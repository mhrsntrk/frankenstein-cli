package auth

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mhrsntrk/frankenstein-cli/internal/config"
)

// Nothing reaches Proton until the user has chosen a client identifier. The
// check has to come before the first request, or the failure arrives as one of
// Proton's opaque rejections instead of the explanation.
func TestLoginRefusesWithoutAnAppVersion(t *testing.T) {
	cfg := config.Defaults()

	if cfg.AppVersion != "" {
		t.Fatalf("defaults carry an app_version (%q)", cfg.AppVersion)
	}

	_, err := Login(context.Background(), cfg, Credentials{
		Username: "someone@example.com",
		Password: []byte("unused"),
	})

	if !errors.Is(err, config.ErrNoAppVersion) {
		t.Fatalf("login error = %v, want ErrNoAppVersion", err)
	}

	// The message has to say what to do about it, not merely what is wrong.
	for _, want := range []string{"app_version", config.ExampleAppVersion, "your"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal never mentions %q:\n%s", want, err)
		}
	}
}
