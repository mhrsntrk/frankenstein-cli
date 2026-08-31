package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"sync"

	"github.com/ProtonMail/go-proton-api"
	"github.com/ProtonMail/gopenpgp/v2/crypto"

	"github.com/mhrsntrk/frankenstein-cli/internal/config"
	"github.com/mhrsntrk/frankenstein-cli/internal/mail"
	"github.com/mhrsntrk/frankenstein-cli/internal/mail/protonmail"
	"github.com/mhrsntrk/frankenstein-cli/internal/protonapi"
)

// HumanVerification carries the challenge a caller must present to the user.
//
// Proton answers a fresh login with 422 code 9001 and a token. The token is
// solved in a browser at verify.proton.me and then replayed on the retry. This
// is why login cannot be a pure terminal flow, and why the --json path reports
// it as a structured result rather than an error.
type HumanVerification struct {
	URL     string   `json:"url"`
	Token   string   `json:"token"`
	Methods []string `json:"methods"`
}

// Error makes HumanVerification usable as an error for callers that just want
// to bail out.
func (h *HumanVerification) Error() string {
	return "human verification required: " + h.URL
}

// Credentials is what a login needs from the user.
type Credentials struct {
	Username string
	Password []byte

	// MailboxPassword is only different from Password in two-password mode.
	// Empty means "same as Password".
	MailboxPassword []byte

	// TOTP is the 2FA code, empty when the account has no TOTP.
	TOTP string

	// HV is a solved human-verification challenge being replayed.
	HV *HumanVerification
}

// Client bundles an authenticated Proton session with its unlocked keys.
//
// Two API clients share the session. go-proton-api does auth, keys, decryption
// and sending; protonapi does conversations, newsletters and event deltas,
// which upstream does not model.
type Client struct {
	Manager *proton.Manager
	Client  *proton.Client
	API     *protonapi.Client

	// mu guards Auth. The auth handler rewrites it on every token rotation,
	// which happens on the library's own goroutine timing, while Session()
	// may be reading it from a persistence callback or the CLI. Read Auth
	// through Session() rather than the field once the client is live.
	mu   sync.Mutex
	Auth proton.Auth

	UserKR    *crypto.KeyRing
	AddrKRs   map[string]*crypto.KeyRing
	Addresses []proton.Address
	User      proton.User

	SaltedKeyPass []byte
}

// Provider wraps the session as a mail.Provider.
func (c *Client) Provider() mail.Provider {
	return protonmail.New(c.Manager, c.Client, c.API, c.UserKR, c.AddrKRs, c.Addresses, true)
}

// Session converts the client into a persistable session.
func (c *Client) Session() Session {
	c.mu.Lock()
	defer c.mu.Unlock()

	return Session{
		UID:           c.Auth.UID,
		RefreshToken:  c.Auth.RefreshToken,
		UserID:        c.Auth.UserID,
		Username:      c.User.Name,
		SaltedKeyPass: c.SaltedKeyPass,
	}
}

// newManager builds a Proton manager configured the way Proton requires.
func newManager(cfg config.Config) (*proton.Manager, error) {
	// Proton's anti-abuse layer correlates the human verification token
	// against a session cookie, so the failed attempt and the retry have to
	// share a jar. The library defaults this to nil; Bridge always sets one.
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("cookie jar: %w", err)
	}

	// Refused here rather than at the API, so the message is ours and says
	// what the choice actually is.
	if strings.TrimSpace(cfg.AppVersion) == "" {
		return nil, appVersionUnset()
	}

	return proton.New(
		proton.WithHostURL(cfg.APIHost),
		proton.WithAppVersion(cfg.AppVersion),
		proton.WithCookieJar(jar),
		// The library logs failed requests to stderr by default, which puts
		// raw RESTY lines in front of a user who should be reading our own
		// error message instead. Errors still surface through the returned
		// error; this only silences the duplicate.
		proton.WithLogger(quietLogger{}),
	), nil
}

// quietLogger discards the library's own request logging.
type quietLogger struct{}

func (quietLogger) Errorf(string, ...any) {}
func (quietLogger) Warnf(string, ...any)  {}
func (quietLogger) Debugf(string, ...any) {}

// Login performs a full SRP login, handling human verification and 2FA, and
// unlocks the account's keys.
//
// When Proton demands human verification and creds.HV is nil, the returned
// error is a *HumanVerification the caller should surface to the user before
// retrying with it set.
func Login(ctx context.Context, cfg config.Config, creds Credentials) (*Client, error) {
	m, err := newManager(cfg)
	if err != nil {
		return nil, err
	}

	var (
		c    *proton.Client
		auth proton.Auth
	)

	if creds.HV != nil {
		c, auth, err = m.NewClientWithLoginWithHVToken(ctx, creds.Username, creds.Password, &proton.APIHVDetails{
			Methods: creds.HV.Methods,
			Token:   creds.HV.Token,
		})
	} else {
		c, auth, err = m.NewClientWithLogin(ctx, creds.Username, creds.Password)
	}

	if err != nil {
		m.Close()

		if hv := asHumanVerification(err); hv != nil {
			return nil, hv
		}

		return nil, fmt.Errorf("login: %w", annotateAppVersion(err))
	}

	if auth.TwoFA.Enabled&proton.HasFIDO2 != 0 && auth.TwoFA.Enabled&proton.HasTOTP == 0 {
		c.Close()
		m.Close()

		return nil, errors.New("this account is FIDO2-only, which go-proton-api cannot do; enable TOTP as well")
	}

	if auth.TwoFA.Enabled&proton.HasTOTP != 0 {
		if creds.TOTP == "" {
			c.Close()
			m.Close()

			return nil, errors.New("this account needs a TOTP code")
		}

		if err := c.Auth2FA(ctx, proton.Auth2FAReq{TwoFactorCode: creds.TOTP}); err != nil {
			c.Close()
			m.Close()

			return nil, fmt.Errorf("two-factor: %w", err)
		}
	}

	mboxPass := creds.MailboxPassword
	if len(mboxPass) == 0 {
		mboxPass = creds.Password
	}

	cl, err := unlock(ctx, cfg, m, c, auth, mboxPass)
	if err != nil {
		c.Close()
		m.Close()

		return nil, err
	}

	return cl, nil
}

// Resume rebuilds a client from the stored session, skipping captcha and 2FA.
//
// It takes the Store rather than a Session on purpose. Proton invalidates a
// refresh token the moment it is exchanged, so a resume that does not write
// the new token back leaves a dead credential behind and the next run has to
// log in from scratch. Making the store mandatory removes the chance to forget.
func Resume(ctx context.Context, cfg config.Config, store *Store) (*Client, error) {
	sess, err := store.Load()
	if err != nil {
		return nil, err
	}

	if !sess.Valid() {
		return nil, ErrNoSession
	}

	m, err := newManager(cfg)
	if err != nil {
		return nil, err
	}

	c, auth, err := m.NewClientWithRefresh(ctx, sess.UID, sess.RefreshToken)
	if err != nil {
		m.Close()

		// Clear only when Proton itself rejected the token. A DNS failure,
		// a timeout or a cancelled context says nothing about the token,
		// and clearing on those would destroy a live session over a flaky
		// network, forcing a full login with captcha and 2FA.
		if shouldClearSession(err) {
			_ = store.Clear()
		}

		return nil, fmt.Errorf("resume session: %w", err)
	}

	if len(sess.SaltedKeyPass) == 0 {
		c.Close()
		m.Close()

		return nil, errors.New("stored session has no key passphrase; log in again")
	}

	user, err := c.GetUser(ctx)
	if err != nil {
		c.Close()
		m.Close()

		return nil, fmt.Errorf("get user: %w", err)
	}

	addrs, err := c.GetAddresses(ctx)
	if err != nil {
		c.Close()
		m.Close()

		return nil, fmt.Errorf("get addresses: %w", err)
	}

	userKR, addrKRs, err := proton.Unlock(user, addrs, sess.SaltedKeyPass, nil)
	if err != nil {
		c.Close()
		m.Close()

		return nil, fmt.Errorf("unlock keys: %w", err)
	}

	client := &Client{
		Manager:       m,
		Client:        c,
		API:           protonapi.New(cfg.APIHost, cfg.AppVersion, auth.UID, auth.AccessToken),
		Auth:          auth,
		UserKR:        userKR,
		AddrKRs:       addrKRs,
		Addresses:     addrs,
		User:          user,
		SaltedKeyPass: sess.SaltedKeyPass,
	}

	client.wireAPIAuth()

	// Persist the rotated token immediately, then on every later rotation.
	// The save is retried once because losing it means losing the only copy
	// of a token Proton has already spent the predecessor of; a transient
	// keyring hiccup should not cost the whole session.
	if err := store.Save(client.Session()); err != nil {
		if err = store.Save(client.Session()); err != nil {
			c.Close()
			m.Close()

			return nil, fmt.Errorf("persist refreshed session: %w", err)
		}
	}

	client.OnSessionChange(func(s Session) { _ = store.Save(s) })

	return client, nil
}

// unlock derives the salted key passphrase and opens the key rings.
func unlock(ctx context.Context, cfg config.Config, m *proton.Manager, c *proton.Client, auth proton.Auth, mboxPass []byte) (*Client, error) {
	user, err := c.GetUser(ctx)
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}

	salts, err := c.GetSalts(ctx)
	if err != nil {
		return nil, fmt.Errorf("get salts: %w", err)
	}

	// user.Keys.Primary() panics when no key is marked primary, which a
	// keyless or half-provisioned account can legitimately produce. Scan by
	// hand so that shape comes back as an error instead of a crash.
	var keyID string

	for _, key := range user.Keys {
		if key.Primary {
			keyID = key.ID
			break
		}
	}

	if keyID == "" {
		return nil, errors.New("this account has no primary key, so the mailbox cannot be unlocked")
	}

	saltedKeyPass, err := salts.SaltForKey(mboxPass, keyID)
	if err != nil {
		return nil, fmt.Errorf("derive key passphrase: %w", err)
	}

	// SaltForKey swallows its own error and returns (nil, nil) on failure, so
	// an empty result here is the real signal that the password was wrong.
	if len(saltedKeyPass) == 0 {
		return nil, errors.New("could not derive the key passphrase; wrong mailbox password?")
	}

	addrs, err := c.GetAddresses(ctx)
	if err != nil {
		return nil, fmt.Errorf("get addresses: %w", err)
	}

	userKR, addrKRs, err := proton.Unlock(user, addrs, saltedKeyPass, nil)
	if err != nil {
		return nil, fmt.Errorf("unlock keys: %w", err)
	}

	cl := &Client{
		Manager:       m,
		Client:        c,
		API:           protonapi.New(cfg.APIHost, cfg.AppVersion, auth.UID, auth.AccessToken),
		Auth:          auth,
		UserKR:        userKR,
		AddrKRs:       addrKRs,
		Addresses:     addrs,
		User:          user,
		SaltedKeyPass: saltedKeyPass,
	}

	cl.wireAPIAuth()

	return cl, nil
}

// wireAPIAuth ties the two API clients' credential lifecycles together, and
// must run once right after the Client is assembled.
//
// The first half pushes every rotation go-proton-api performs into the side
// client, so it never keeps serving a token Proton has retired. The second
// half covers the reverse gap: the side client cannot refresh a session on
// its own (only go-proton-api holds the refresh token), so on a 401 it asks
// us to force a request through the upstream client. That request trips the
// library's own 401 recovery, which fires the handler registered here, which
// updates the side client before its retry.
func (c *Client) wireAPIAuth() {
	// Registered before any OnSessionChange handler on purpose: handlers run
	// in registration order, so Auth is current by the time a persistence
	// callback reads it through Session().
	c.Client.AddAuthHandler(func(a proton.Auth) {
		c.mu.Lock()
		c.Auth = a
		c.mu.Unlock()

		c.API.SetAuth(a.UID, a.AccessToken)
	})

	c.API.SetRefreshFunc(func(ctx context.Context) error {
		// GetUser is the cheapest request the upstream client always has
		// the right to make; its only job here is to hit the 401 path.
		_, err := c.Client.GetUser(ctx)
		return err
	})
}

// shouldClearSession decides whether a failed refresh exchange means the
// stored session is dead. This is the line between "log in again" and
// "destroyed a live session because the wifi blinked", so it is deliberately
// a pure function with the table pinned by tests.
func shouldClearSession(err error) bool {
	// A cancelled or timed-out attempt proves nothing about the token.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	// Transport failures (DNS, TLS, refused connections) never reach Proton,
	// so the token was not judged at all.
	var apiErr *proton.APIError
	if !errors.As(err, &apiErr) {
		return false
	}

	switch apiErr.Status {
	case http.StatusUnauthorized:
		return true
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		// Proton reports a spent or revoked refresh token as 400/422 with
		// code 10013. Other errors under the same statuses — a retired app
		// version is the common one — leave the token usable once the
		// client-side problem is fixed, so the session must survive them.
		return apiErr.Code == proton.AuthRefreshTokenInvalid
	default:
		return false
	}
}

// asHumanVerification converts a 9001 API error into a challenge.
func asHumanVerification(err error) *HumanVerification {
	var apiErr *proton.APIError

	if !errors.As(err, &apiErr) || !apiErr.IsHVError() {
		return nil
	}

	details, derr := apiErr.GetHVDetails()
	if derr != nil {
		return nil
	}

	return &HumanVerification{
		Token:   details.Token,
		Methods: details.Methods,
		URL: fmt.Sprintf("https://verify.proton.me/?methods=%s&token=%s",
			strings.Join(details.Methods, ","), details.Token),
	}
}

// appVersionUnset explains the one piece of configuration this tool cannot
// choose on anyone's behalf.
//
// Proton accepts only identifiers belonging to its own clients, so the
// working values are all the name of software this is not. Sending one is a
// decision about the reader's account and Proton's terms, and it is theirs to
// make knowingly rather than to discover in a default.
func appVersionUnset() error {
	path, err := config.Path()
	if err != nil {
		path = "the config file"
	}

	return fmt.Errorf("%w\n\n"+
		"Proton only accepts client identifiers belonging to its own applications, so\n"+
		"this tool cannot supply one of its own. Setting app_version means presenting\n"+
		"to Proton as one of their clients, which their terms do not allow, and the\n"+
		"account at risk is yours.\n\n"+
		"To proceed, add it to %s:\n\n"+
		"    {\"app_version\": %q}\n\n"+
		"See the README section on identifying to Proton",
		config.ErrNoAppVersion, path, config.ExampleAppVersion)
}

// annotateAppVersion turns Proton's opaque version rejections into an error
// that says what to actually do about it.
func annotateAppVersion(err error) error {
	var apiErr *proton.APIError
	if !errors.As(err, &apiErr) {
		return err
	}

	switch apiErr.Code {
	case 5003:
		return fmt.Errorf("%w\n\nProton has retired the client version app_version names. "+
			"Set it to a current release and try again", err)
	case 2064:
		return fmt.Errorf("%w\n\nProton rejected the client identifier. app_version must look like "+
			"<platform>-bridge@<version>, e.g. %s", err, config.ExampleAppVersion)
	default:
		return err
	}
}

// OnSessionChange registers a callback fired whenever Proton hands back new
// tokens. Proton rotates the refresh token on every use, so the stored session
// must be rewritten each time or it becomes a dead credential.
//
// This lives here rather than in the caller so that no package outside
// internal/auth and internal/mail/protonmail has to import a Proton type.
// Propagating the new tokens into the client itself is not this method's job;
// wireAPIAuth registers that handler at construction, before any of these.
func (c *Client) OnSessionChange(fn func(Session)) {
	c.Client.AddAuthHandler(func(proton.Auth) {
		fn(c.Session())
	})
}
