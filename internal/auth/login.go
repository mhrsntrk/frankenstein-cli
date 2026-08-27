package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http/cookiejar"
	"strings"

	"github.com/ProtonMail/go-proton-api"
	"github.com/ProtonMail/gopenpgp/v2/crypto"

	"github.com/mhrsntrk/frankenstein-cli/internal/config"
	"github.com/mhrsntrk/frankenstein-cli/internal/mail"
	"github.com/mhrsntrk/frankenstein-cli/internal/mail/protonmail"
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

// Client bundles an authenticated Proton client with its unlocked keys.
type Client struct {
	Manager   *proton.Manager
	Client    *proton.Client
	Auth      proton.Auth
	UserKR    *crypto.KeyRing
	AddrKRs   map[string]*crypto.KeyRing
	Addresses []proton.Address
	User      proton.User

	SaltedKeyPass []byte
}

// Provider wraps the client as a mail.Provider.
func (c *Client) Provider() mail.Provider {
	return protonmail.New(c.Manager, c.Client, c.UserKR, c.AddrKRs, c.Addresses, true)
}

// Session converts the client into a persistable session.
func (c *Client) Session() Session {
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

	return proton.New(
		proton.WithHostURL(cfg.APIHost),
		proton.WithAppVersion(cfg.AppVersion),
		proton.WithCookieJar(jar),
	), nil
}

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

	cl, err := unlock(ctx, m, c, auth, mboxPass)
	if err != nil {
		c.Close()
		m.Close()

		return nil, err
	}

	return cl, nil
}

// Resume rebuilds a client from a stored session, skipping captcha and 2FA.
func Resume(ctx context.Context, cfg config.Config, sess Session) (*Client, error) {
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

	return &Client{
		Manager:       m,
		Client:        c,
		Auth:          auth,
		UserKR:        userKR,
		AddrKRs:       addrKRs,
		Addresses:     addrs,
		User:          user,
		SaltedKeyPass: sess.SaltedKeyPass,
	}, nil
}

// unlock derives the salted key passphrase and opens the key rings.
func unlock(ctx context.Context, m *proton.Manager, c *proton.Client, auth proton.Auth, mboxPass []byte) (*Client, error) {
	user, err := c.GetUser(ctx)
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}

	salts, err := c.GetSalts(ctx)
	if err != nil {
		return nil, fmt.Errorf("get salts: %w", err)
	}

	keyID := user.Keys.Primary().ID

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

	return &Client{
		Manager:       m,
		Client:        c,
		Auth:          auth,
		UserKR:        userKR,
		AddrKRs:       addrKRs,
		Addresses:     addrs,
		User:          user,
		SaltedKeyPass: saltedKeyPass,
	}, nil
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

// annotateAppVersion turns Proton's opaque version rejections into an error
// that says what to actually do about it.
func annotateAppVersion(err error) error {
	var apiErr *proton.APIError
	if !errors.As(err, &apiErr) {
		return err
	}

	switch apiErr.Code {
	case 5003:
		return fmt.Errorf("%w\n\nProton has retired the client version this tool identifies as (%s). "+
			"Set app_version in the config to the current Proton Bridge release and try again",
			err, config.DefaultAppVersion)
	case 2064:
		return fmt.Errorf("%w\n\nProton rejected the client identifier. app_version must look like "+
			"<platform>-bridge@<version>, e.g. %s", err, config.DefaultAppVersion)
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
func (c *Client) OnSessionChange(fn func(Session)) {
	c.Client.AddAuthHandler(func(a proton.Auth) {
		c.Auth = a
		fn(c.Session())
	})
}
