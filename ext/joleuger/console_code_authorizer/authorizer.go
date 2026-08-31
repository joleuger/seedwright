// Package console_code_authorizer implements a control-plane authorizer
// using the OAuth Device Authorization Grant (RFC 8628) pattern: generate
// a short-lived, single-use code; surface it on a channel only someone with
// genuine console access can see; accept it back through a different channel.
//
// See EXTENSION.md for the full extension contract and safety notes.
package console_code_authorizer

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"log/slog"
	"math/big"
	"net/http"
	"sync"
	"time"

	"seedwright/internal/authz"
)

const (
	codeLen  = 8
	expiry   = 10 * time.Minute
	alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
)

// Authorizer implements authz.ControlPlaneAuthenticator using the console-code
// pattern. It generates short, high-entropy codes that are logged to stdout
// and accepted via a form POST. Single-use: a successful match invalidates
// the code immediately.
type Authorizer struct {
	mu        sync.Mutex
	code      string
	expiresAt time.Time
}

// New creates a fresh Authorizer.
func New() *Authorizer {
	return &Authorizer{}
}

// generateCode creates a new short, high-entropy code (8 random
// alphanumeric characters — the same order of magnitude GitHub's own device
// flow uses), logs it to stdout, and starts a short expiry window (10
// minutes).
func (a *Authorizer) generateCode() string {
	a.mu.Lock()
	defer a.mu.Unlock()

	b := make([]byte, codeLen)
	for i := range b {
		n, err := rand.Int(rand.Reader, bigN())
		if err != nil {
			// Fallback — extremely unlikely.
			b[i] = alphabet[time.Now().UnixNano()%int64(len(alphabet))]
		} else {
			b[i] = alphabet[n.Int64()]
		}
	}

	a.code = string(b)
	a.expiresAt = time.Now().Add(expiry)

	slog.Warn(
		"console_code_authorizer: ownership claim code generated",
		"code", a.code,
		"expires", a.expiresAt.Format(time.RFC3339),
	)

	return a.code
}

// bigN returns a big.Int equal to len(alphabet), used for modular random
// selection via crypto/rand.Int.
func bigN() *big.Int {
	return big.NewInt(int64(len(alphabet)))
}

// Authenticate reads a submitted code directly from the request form value and
// checks it against the current code and expiry. Single-use: a successful
// match invalidates the code immediately.
func (a *Authorizer) Authenticate(_ context.Context, r *http.Request) bool {
	a.mu.Lock()
	defer a.mu.Unlock()

	submitted := r.FormValue("code")
	if submitted == "" || a.code == "" || time.Now().After(a.expiresAt) {
		return false
	}

	// Constant-time comparison to prevent timing attacks.
	if subtle.ConstantTimeCompare([]byte(submitted), []byte(a.code)) != 1 {
		return false
	}

	// Invalidate after use (single-use).
	a.code = ""

	return true
}

// Code returns the current code (if any) for display on the extension's own
// page. Returns empty string if no valid code exists. The caller is
// responsible for generating a new one if needed.
func (a *Authorizer) Code() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.code
}

// HasExpired returns whether the current code has expired (if one exists).
func (a *Authorizer) HasExpired() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.code != "" && time.Now().After(a.expiresAt)
}

// Compile-time check: Authorizer implements ControlPlaneAuthenticator.
var _ authz.ControlPlaneAuthenticator = (*Authorizer)(nil)
