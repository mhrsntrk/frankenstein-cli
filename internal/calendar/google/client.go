package google

// Built-in OAuth client, injected at build time:
//
//	-ldflags "-X .../internal/calendar/google.DefaultClientID=...
//	          -X .../internal/calendar/google.DefaultClientSecret=..."
//
// Both are empty in the repository, so a build from source asks the user for
// their own. See docs/calendar-setup.md for why that is the default and what
// shipping one costs.
//
// Google does not treat a desktop client secret as confidential -- its own
// guidance says these apps "cannot keep secrets" -- so embedding one is not a
// leak. The reasons to hesitate are different ones: an unverified client shows
// a warning screen, verification is tied to a real domain and privacy policy,
// and whoever owns the client becomes the support contact for every user.
var (
	DefaultClientID     string
	DefaultClientSecret string
)

// Credentials resolves which OAuth client to use: the one in the user's config
// if they set one, otherwise whatever the build carries.
func Credentials(configuredID, configuredSecret string) (id, secret string) {
	if configuredID != "" {
		return configuredID, configuredSecret
	}

	return DefaultClientID, DefaultClientSecret
}
