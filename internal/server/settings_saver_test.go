package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"seedwright/internal/data/model"
	"strings"
	"sync"
	"testing"
)

// stubSaver records the fields it was called with.
type stubSaver struct {
	mu     sync.Mutex
	fields map[string]any
	err    error
}

func (s *stubSaver) call(ctx context.Context, project string, fields map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fields = fields
	return s.err
}

func (s *stubSaver) got() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fields
}

func postSettings(t *testing.T, h *handler, mux *http.ServeMux, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/test-project/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	// Route through the mux: the handler reads :project via r.PathValue.
	mux.ServeHTTP(rec, req)
	return rec
}

func TestScopedSave_DispatchesToSaver(t *testing.T) {
	h, mux := setupHandler(t)
	stub := &stubSaver{}
	h.cfg.Hooks = &Hooks{
		SettingsSavers: map[string]SettingsSaver{
			"joleuger/photobooth": stub.call,
		},
	}

	rec := postSettings(t, h, mux, `{"section":"joleuger/photobooth","fields":{"max_photos":"3","print_enabled":false}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	got := stub.got()
	if got == nil {
		t.Fatal("saver was not called")
	}
	if got["max_photos"] != "3" || got["print_enabled"] != false {
		t.Errorf("saver fields = %v, want the raw submitted map", got)
	}
}

func TestScopedSave_NoSaver_Returns400(t *testing.T) {
	h, mux := setupHandler(t)
	// setupHandler leaves Hooks nil — no saver registered for anything.
	rec := postSettings(t, h, mux, `{"section":"joleuger/photobooth","fields":{"max_photos":"3"}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestScopedSave_ValidationError_Returns400(t *testing.T) {
	h, mux := setupHandler(t)
	stub := &stubSaver{err: &ValidationError{Message: "max_photos: must be an integer"}}
	h.cfg.Hooks = &Hooks{SettingsSavers: map[string]SettingsSaver{"joleuger/photobooth": stub.call}}

	rec := postSettings(t, h, mux, `{"section":"joleuger/photobooth","fields":{"max_photos":"many"}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "max_photos: must be an integer") {
		t.Errorf("body %q does not contain the validation message", rec.Body.String())
	}
}

func TestScopedSave_SaverError_Returns500(t *testing.T) {
	h, mux := setupHandler(t)
	stub := &stubSaver{err: errors.New("storage down")}
	h.cfg.Hooks = &Hooks{SettingsSavers: map[string]SettingsSaver{"joleuger/photobooth": stub.call}}

	rec := postSettings(t, h, mux, `{"section":"joleuger/photobooth","fields":{"max_photos":"3"}}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestLegacySave_DispatchesToSaver(t *testing.T) {
	h, mux := setupHandler(t)
	stub := &stubSaver{}
	h.cfg.Hooks = &Hooks{SettingsSavers: map[string]SettingsSaver{"joleuger/photobooth": stub.call}}

	// Legacy full-payload format: extension_settings is a map of
	// "owner/extension" → fields. Unknown sections are skipped
	// (best-effort legacy semantics) without failing the request.
	rec := postSettings(t, h, mux, `{
		"extension_settings": {
			"joleuger/photobooth": {"keep_on_cancel": true},
			"joleuger/unknown": {"foo": "bar"}
		}
	}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	got := stub.got()
	if got == nil || got["keep_on_cancel"] != true {
		t.Errorf("saver fields = %v, want the photobooth fields", got)
	}
}

// TestSettingsPage_EmbedsLowercaseFieldMetadata guards the settings page
// JS contract: saveSection() reads f.key / f.type from the embedded
// `const sections = {...}` object, so FieldInfo must serialize with
// lowercase json keys. Without the tags the browser saw f.key ===
// undefined, matched no inputs, and POSTed an empty fields map.
func TestSettingsPage_EmbedsLowercaseFieldMetadata(t *testing.T) {
	h, _ := setupHandler(t)
	// Dedicated mux: the shared test mux's legacy patterns conflict with
	// the /basic/ route namespace.
	mux := http.NewServeMux()
	h.cfg.Hooks = &Hooks{
		SettingsSection: []func(context.Context, string, model.ProjectSettingsDelta) (*Section, error){
			func(ctx context.Context, project string, delta model.ProjectSettingsDelta) (*Section, error) {
				return &Section{
					ID:    "joleuger/photobooth",
					Label: "Photobooth",
					HTML:  `<input data-section="joleuger/photobooth" data-field="max_photos" value="5">`,
					Fields: []FieldInfo{
						{Key: "max_photos", Type: "number"},
						{Key: "print_enabled", Type: "checkbox"},
					},
				}, nil
			},
		},
	}
	mux.Handle("GET /basic/{project}/settings", http.HandlerFunc(h.projectSettingsPage))

	req := httptest.NewRequest(http.MethodGet, "/basic/test-project/settings", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, `"Key":`) || strings.Contains(body, `"Type":`) {
		t.Error("embedded sections object uses capitalized field keys — the browser JS reads f.key/f.type")
	}
	if !strings.Contains(body, `"key":"max_photos","type":"number"`) {
		t.Error("embedded sections object missing lowercase max_photos field metadata")
	}
	if !strings.Contains(body, `"key":"print_enabled","type":"checkbox"`) {
		t.Error("embedded sections object missing lowercase print_enabled field metadata")
	}
}
