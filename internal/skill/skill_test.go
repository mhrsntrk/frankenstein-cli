package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallWritesTheSkill(t *testing.T) {
	dir := t.TempDir()

	path, err := Install(dir, false)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}

	if path != filepath.Join(dir, "SKILL.md") {
		t.Errorf("path = %q", path)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}

	want, err := Content()
	if err != nil {
		t.Fatalf("Content: %v", err)
	}

	if string(got) != string(want) {
		t.Error("installed skill does not match the embedded one")
	}
}

// A user who edited their copy must not lose it to a routine upgrade: without
// --force an existing file survives untouched.
func TestInstallRefusesToOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SKILL.md")

	if err := os.WriteFile(path, []byte("edited by hand"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := Install(dir, false)
	if err == nil {
		t.Fatal("Install overwrote without --force")
	}

	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("err = %v, want it to name --force", err)
	}

	// The refusal still says where the file is.
	if got != path {
		t.Errorf("path on refusal = %q, want %q", got, path)
	}

	b, _ := os.ReadFile(path)
	if string(b) != "edited by hand" {
		t.Errorf("existing file was changed to %q", b)
	}
}

func TestInstallForceOverwrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SKILL.md")

	if err := os.WriteFile(path, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Install(dir, true); err != nil {
		t.Fatalf("Install --force: %v", err)
	}

	got, _ := os.ReadFile(path)

	want, _ := Content()
	if string(got) != string(want) {
		t.Error("--force did not replace the file with the embedded skill")
	}
}
