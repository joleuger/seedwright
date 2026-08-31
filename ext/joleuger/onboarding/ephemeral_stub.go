//go:build !linux

package onboarding

// ephemeralStorageWarning is a no-op outside Linux: filesystem magic
// numbers are a Linux detail, and on Windows (the other documented
// platform) there is no equivalent signal to read.
func ephemeralStorageWarning(string) string { return "" }
