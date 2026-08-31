//go:build linux

package onboarding

import (
	"path/filepath"
	"syscall"
)

// Linux filesystem magic numbers (see /usr/include/linux/magic.h).
const (
	magicTmpfs   = int64(0x01021994)
	magicOverlay = int64(0x794c7630)
)

// ephemeralWarningFor maps a filesystem magic number to a human-facing
// warning, or "" for persistent (or unrecognized) filesystems. Split out
// from the syscall so the mapping is testable without a real tmpfs.
func ephemeralWarningFor(stype int64) string {
	switch stype {
	case magicTmpfs:
		return "The config path sits on tmpfs — the written config.yaml disappears on reboot. Consider mounting a real folder there, or use Download."
	case magicOverlay:
		return "The config path sits on overlayfs — in a container that is usually the ephemeral root filesystem. Unless this path is on a mounted volume, the written config.yaml is lost when the container is recreated; consider mounting a volume, or use Download."
	}
	return ""
}

// ephemeralStorageWarning warns when the directory holding path lives on
// ephemeral storage (tmpfs, overlayfs). Returns "" otherwise or when the
// filesystem cannot be determined.
func ephemeralStorageWarning(path string) string {
	var st syscall.Statfs_t
	if err := syscall.Statfs(filepath.Dir(path), &st); err != nil {
		return ""
	}
	return ephemeralWarningFor(st.Type)
}
