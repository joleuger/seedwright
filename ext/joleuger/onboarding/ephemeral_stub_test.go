//go:build !linux

package onboarding

import "testing"

// On non-Linux platforms (Windows, macOS) ephemeral detection is a
// documented no-op — the warning must stay empty.
func TestEphemeralWarningStubIsNoop(t *testing.T) {
	if got := ephemeralStorageWarning("/anywhere/config.yaml"); got != "" {
		t.Errorf("non-linux stub must return \"\", got %q", got)
	}
}
