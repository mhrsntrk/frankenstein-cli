package auth

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"

	"github.com/ProtonMail/go-proton-api"
)

// TestShouldClearSession pins the clear-vs-keep table for a failed refresh
// exchange. Clearing on the wrong side destroys a live session over a flaky
// network; keeping on the wrong side leaves the user re-running a command
// that can never work.
func TestShouldClearSession(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"context canceled", context.Canceled, false},
		{"wrapped deadline", fmt.Errorf("resume: %w", context.DeadlineExceeded), false},
		{"transport failure", &net.OpError{Op: "dial", Err: errors.New("connection refused")}, false},
		{"plain error", errors.New("dns lookup failed"), false},
		{"401", &proton.APIError{Status: 401}, true},
		{"400 invalid refresh token", &proton.APIError{Status: 400, Code: proton.AuthRefreshTokenInvalid}, true},
		{"422 invalid refresh token", &proton.APIError{Status: 422, Code: proton.AuthRefreshTokenInvalid}, true},
		{"400 retired app version", &proton.APIError{Status: 400, Code: proton.AppVersionBadCode}, false},
		{"422 other code", &proton.APIError{Status: 422, Code: proton.InvalidValue}, false},
		{"429 throttled", &proton.APIError{Status: 429}, false},
		{"500", &proton.APIError{Status: 500}, false},
		{"wrapped api error", fmt.Errorf("resume session: %w", &proton.APIError{Status: 401}), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldClearSession(tc.err); got != tc.want {
				t.Errorf("shouldClearSession(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
