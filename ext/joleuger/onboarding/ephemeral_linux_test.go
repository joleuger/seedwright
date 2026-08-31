//go:build linux

package onboarding

import "testing"

func TestEphemeralWarningFor(t *testing.T) {
	if got := ephemeralWarningFor(magicTmpfs); got == "" {
		t.Error("tmpfs must produce a warning")
	}
	if got := ephemeralWarningFor(magicOverlay); got == "" {
		t.Error("overlayfs must produce a warning")
	}
	// ext4 (and the like) are durable — no warning.
	if got := ephemeralWarningFor(0xEF53); got != "" {
		t.Errorf("ext4 must not warn, got %q", got)
	}
}
