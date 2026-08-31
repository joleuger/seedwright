package server

import (
	"log/slog"
	"net/http"
	"net/url"
	"strings"
)

// stripPrefix wraps an http.Handler with a middleware that removes
// the configured path prefix from incoming requests before passing
// them to the inner handler.
//
// When prefix is empty the wrapper is a no-op — the inner handler
// receives the original request unchanged. This makes the default
// (prefix-less) deployment identical to today's behaviour with
// zero additional checks.
func stripPrefix(prefix string, inner http.Handler) http.Handler {
	return NewStripPrefix(prefix, inner)
}

// NewStripPrefix is the exported version of stripPrefix. It wraps an
// http.Handler by removing the configured path prefix from incoming
// requests before passing them to the inner handler.
//
// When prefix is empty the wrapper is a no-op — the inner handler
// receives the original request unchanged. This makes the default
// (prefix-less) deployment identical to today's behaviour with
// zero additional checks.
func NewStripPrefix(prefix string, inner http.Handler) http.Handler {
	if prefix == "" {
		return inner
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, prefix) {
			// Request doesn't start with the configured prefix —
			// reject. A reverse-proxy should only forward the
			// subpath the app is meant to serve.
			http.NotFound(w, r)
			return
		}
		// Clone the request so we don't mutate the original.
		r2 := new(http.Request)
		*r2 = *r
		r2.URL = new(url.URL)
		*r2.URL = *r.URL

		r2.URL.Path = strings.TrimPrefix(r.URL.Path, prefix)
		r2.URL.RawPath = strings.TrimPrefix(r.URL.RawPath, prefix)
		r2.RequestURI = "" // let net/http recompute

		inner.ServeHTTP(w, r2)
	})
}

// NewDebugLogging wraps an http.Handler with middleware that logs
// every incoming request at DEBUG level. Returns a no-op wrapper when
// debug is false.
func NewDebugLogging(inner http.Handler, debug bool) http.Handler {
	if !debug {
		return inner
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slog.Debug("http-request",
			"method", r.Method,
			"path", r.URL.Path,
			"remote", r.RemoteAddr,
		)
		inner.ServeHTTP(w, r)
		slog.Debug("http-response",
			"method", r.Method,
			"path", r.URL.Path,
			"remote", r.RemoteAddr,
			// We can't read the status code from ResponseWriter,
			// but we know 404 means the route wasn't matched.
			"status", "check-404",
		)
	})
}
