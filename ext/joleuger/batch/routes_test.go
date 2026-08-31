package batch

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"seedwright/internal/app"
)

// setupTestMux creates a ServeMux, registers the batch extension's routes,
// and returns the extension and mux. The preview handler only needs
// e.Expand() — it doesn't use DB, storage, or any App fields.
func setupTestMux(t *testing.T) (*Extension, *http.ServeMux) {
	t.Helper()
	e := &Extension{} // only Expand is called by handlePreview
	mux := http.NewServeMux()
	e.mux = mux
	// RegisterRoutes calls e.mux.HandleFunc — it doesn't use the App parameter.
	e.RegisterRoutes(&app.App{})
	return e, mux
}

func TestHandlePreview_JSONBody(t *testing.T) {
	e, mux := setupTestMux(t)
	_ = e

	body := `{"prompt": "a cat", "seeds": "42"}`
	req := httptest.NewRequest(http.MethodPost, "/api/test-project/ext/joleuger/batch/preview", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}

	if int(resp["count"].(float64)) != 1 {
		t.Errorf("count = %v, want 1", resp["count"])
	}
}

func TestHandlePreview_ComboSyntax(t *testing.T) {
	e, mux := setupTestMux(t)
	_ = e

	body := `{"prompt": "A {cat,dog}", "seeds": "42"}`
	req := httptest.NewRequest(http.MethodPost, "/api/myproj/ext/joleuger/batch/preview", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}

	if int(resp["count"].(float64)) != 2 {
		t.Errorf("count = %v, want 2", resp["count"])
	}

	variants := resp["variants"].([]any)
	if len(variants) != 2 {
		t.Fatalf("variants = %d items, want 2", len(variants))
	}
}

func TestHandlePreview_ComboPlusSeeds(t *testing.T) {
	e, mux := setupTestMux(t)
	_ = e

	// 2 prompt groups × 3 seeds = 6 variants.
	body := `{"prompt": "A {cat,dog}", "seeds": "1,2,3"}`
	req := httptest.NewRequest(http.MethodPost, "/api/proj/ext/joleuger/batch/preview", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}

	if int(resp["count"].(float64)) != 6 {
		t.Errorf("count = %v, want 6 (2×3)", resp["count"])
	}
}

func TestHandlePreview_InvalidJSON(t *testing.T) {
	e, mux := setupTestMux(t)
	_ = e

	// Malformed JSON should return 400.
	req := httptest.NewRequest(http.MethodPost, "/api/proj/ext/joleuger/batch/preview", bytes.NewBufferString("not json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandlePreview_WrongMethod(t *testing.T) {
	e, mux := setupTestMux(t)
	_ = e

	// GET should return 405.
	req := httptest.NewRequest(http.MethodGet, "/api/proj/ext/joleuger/batch/preview", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandlePreview_EmptyPrompt(t *testing.T) {
	e, mux := setupTestMux(t)
	_ = e

	// Empty prompt is valid — Expand returns a single variant with default seed -1.
	body := `{"prompt": "", "seeds": ""}`
	req := httptest.NewRequest(http.MethodPost, "/api/proj/ext/joleuger/batch/preview", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	// Should succeed — the endpoint doesn't validate prompt content.
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}

	if int(resp["count"].(float64)) != 1 {
		t.Errorf("count = %v, want 1", resp["count"])
	}
}

func TestHandlePreview_ResponseSchema(t *testing.T) {
	e, mux := setupTestMux(t)
	_ = e

	body := `{"prompt": "a cat", "seeds": "42"}`
	req := httptest.NewRequest(http.MethodPost, "/api/proj/ext/joleuger/batch/preview", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	// Verify Content-Type header.
	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}

	// Verify response schema: count (number), variants (array of {prompt, seed}).
	var resp struct {
		Count    int               `json:"count"`
		Variants []json.RawMessage `json:"variants"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response parse error: %v", err)
	}

	if resp.Count != 1 {
		t.Errorf("count = %d, want 1", resp.Count)
	}

	if len(resp.Variants) != 1 {
		t.Fatalf("variants = %d items, want 1", len(resp.Variants))
	}

	// Each variant must have "prompt" and "seed" keys.
	var vmap map[string]any
	if err := json.Unmarshal(resp.Variants[0], &vmap); err != nil {
		t.Fatalf("variant parse error: %v", err)
	}
	if _, ok := vmap["prompt"]; !ok {
		t.Error("variant missing 'prompt' key")
	}
	if _, ok := vmap["seed"]; !ok {
		t.Error("variant missing 'seed' key")
	}
}
