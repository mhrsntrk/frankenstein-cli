package auth

import (
	"context"

	"github.com/mhrsntrk/frankenstein-cli/internal/config"
)

// Logout revokes the stored session on Proton's side, then removes the local
// copy.
//
// Revocation is best-effort by design. The refresh token has to be exchanged
// for an access token before /auth/v4 can be called, and either step can fail
// for reasons that do not matter to a user who asked to log out — the network
// is down, or the token is already dead. The local clear is the part that
// must succeed: after Logout returns nil, no credential remains on this
// machine, and at worst Proton's session list carries an entry the user can
// revoke from the web UI.
func Logout(ctx context.Context, cfg config.Config, store *Store) error {
	if sess, err := store.Load(); err == nil && sess.Valid() {
		if m, merr := newManager(cfg); merr == nil {
			if c, _, rerr := m.NewClientWithRefresh(ctx, sess.UID, sess.RefreshToken); rerr == nil {
				_ = c.AuthDelete(ctx)
				c.Close()
			}

			m.Close()
		}
	}

	return store.Clear()
}
