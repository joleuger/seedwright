package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStripPrefix_NoPrefix(t *testing.T) {
	// When prefix is empty, the middleware should be a no-op.
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})
	handler := stripPrefix("", inner)

	req := httptest.NewRequest(http.MethodGet, "/basic/myproject", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if rec.Body.String() != "ok" {
		t.Errorf("body = %q, want %q", rec.Body.String(), "ok")
	}
}

func TestStripPrefix_WithPrefix(t *testing.T) {
	// When prefix is "/sd", requests to "/sd/basic/foo" should be
	// routed as if they were "/basic/foo".
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("path:" + r.URL.Path))
	})
	handler := stripPrefix("/sd", inner)

	req := httptest.NewRequest(http.MethodGet, "/sd/basic/myproject", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if rec.Body.String() != "path:/basic/myproject" {
		t.Errorf("body = %q, want %q", rec.Body.String(), "path:/basic/myproject")
	}
}

func TestStripPrefix_UnknownPath(t *testing.T) {
	// A request that doesn't start with the prefix should get 404.
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("should not reach"))
	})
	handler := stripPrefix("/sd", inner)

	req := httptest.NewRequest(http.MethodGet, "/other/path", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestStripPrefix_MethodPreserved(t *testing.T) {
	// POST should remain POST after stripping the prefix.
	var method string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
	})
	handler := stripPrefix("/sd", inner)

	req := httptest.NewRequest(http.MethodPost, "/sd/api/myproject/create", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if method != http.MethodPost {
		t.Errorf("method = %q, want %q", method, http.MethodPost)
	}
}
