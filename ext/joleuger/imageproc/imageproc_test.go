package imageproc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"seedwright/internal/app"
	"seedwright/internal/config"
	"seedwright/internal/data/model"
	"seedwright/internal/storage"
)

// --- config ---

const baseConfigYAML = `
server:
  listen: ":8080"
sdcpp:
  backends:
    - name: "default"
      base_url: "http://localhost:3000"
storage:
  endpoint: "https://s3.example.com"
  region: "us-east-1"
  bucket: "images"
  access_key: "AKIA"
  secret_key: "secret"
database:
  sqlite_path: "cache.db"
`

// tempConfig builds a full app config with the given extensions block
// ("" = no extensions section at all).
func tempConfig(t *testing.T, extensionsYAML string) *config.Config {
	t.Helper()
	yaml := baseConfigYAML
	if extensionsYAML != "" {
		yaml += "extensions:\n" + extensionsYAML
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	return cfg
}

func TestLoadConfig_EngineSelection(t *testing.T) {
	tests := []struct {
		name           string
		extensionsYAML string
		wantEngine     string
		wantEnabled    bool
		wantErr        bool
	}{
		{"no config for extension defaults to gm", "", "gm", true, false},
		{"explicit gm", "  joleuger/imageproc:\n    engine: gm\n", "gm", true, false},
		{"none engine", "  joleuger/imageproc:\n    engine: none\n", "none", true, false},
		{"disabled", "  joleuger/imageproc:\n    enabled: false\n", "gm", false, false},
		{"unknown engine is a config error", "  joleuger/imageproc:\n    engine: magick\n", "", false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := LoadConfig(tempConfig(t, tt.extensionsYAML))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %+v", c)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if c.Engine != tt.wantEngine {
				t.Errorf("engine = %q, want %q", c.Engine, tt.wantEngine)
			}
			if c.Enabled != tt.wantEnabled {
				t.Errorf("enabled = %v, want %v", c.Enabled, tt.wantEnabled)
			}
		})
	}
}

func TestNewProcessor_EngineSelection(t *testing.T) {
	cases := map[string]string{
		"  joleuger/imageproc:\n    engine: gm\n":   "gm",
		"  joleuger/imageproc:\n    engine: none\n": "none",
	}
	for extYAML, wantName := range cases {
		p, err := NewProcessor(tempConfig(t, extYAML))
		if err != nil {
			t.Fatalf("NewProcessor: %v", err)
		}
		if p.Name() != wantName {
			t.Errorf("processor name = %q, want %q", p.Name(), wantName)
		}
	}
}

// --- params validation (no defaults) ---

func TestParams_Validation_NoDefaults(t *testing.T) {
	valid := Params{Width: 100, Height: 80, Fit: "crop", Rotate: "auto"}
	if err := valid.validate(); err != nil {
		t.Fatalf("valid params rejected: %v", err)
	}
	if err := (Params{100, 80, "fit", "never"}).validate(); err != nil {
		t.Fatalf("valid params (fit/never) rejected: %v", err)
	}

	tests := []struct {
		name    string
		params  Params
		wantErr string // substring
	}{
		{"zero width", Params{0, 80, "crop", "auto"}, "width"},
		{"negative width", Params{-10, 80, "crop", "auto"}, "width"},
		{"zero height", Params{100, 0, "crop", "auto"}, "height"},
		{"all zero is not defaulted", Params{0, 0, "", ""}, "width"},
		{"bad fit", Params{100, 80, "stretch", "auto"}, "fit"},
		{"empty fit", Params{100, 80, "", "auto"}, "fit"},
		{"bad rotate", Params{100, 80, "crop", "always"}, "rotate"},
		{"empty rotate", Params{100, 80, "crop", ""}, "rotate"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.params.validate()
			if err == nil {
				t.Fatalf("params %+v accepted, want error (no defaults)", tt.params)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want it to mention %q", err, tt.wantErr)
			}
		})
	}
}

// --- none processor: passthrough ---

func TestNoneProcessor_Passthrough(t *testing.T) {
	p := noneProcessor{}
	if p.Name() != "none" {
		t.Errorf("Name() = %q, want none", p.Name())
	}
	if !p.Available() {
		t.Error("Available() = false, want true (no binary needed)")
	}

	src := filepath.Join(t.TempDir(), "src.png")
	if err := os.WriteFile(src, []byte("img"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := p.Process(context.Background(), src, Params{100, 80, "crop", "auto"})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if out != src {
		t.Errorf("Process = %q, want passthrough of %q", out, src)
	}

	// Passthrough is never for bad params.
	if _, err := p.Process(context.Background(), src, Params{}); err == nil {
		t.Error("Process with zero params: expected error, got passthrough")
	}
}

// --- gm processor (skip when gm is absent) ---

func skipIfNoGm(t *testing.T) {
	if _, err := exec.LookPath("gm"); err != nil {
		t.Skip("gm not on PATH")
	}
}

// gmWriteTo runs `gm convert <args...> dst`.
func gmWriteTo(t *testing.T, dst string, args ...string) {
	t.Helper()
	full := append([]string{"convert"}, args...)
	full = append(full, dst)
	out, err := exec.Command("gm", full...).CombinedOutput()
	if err != nil {
		t.Fatalf("gm convert %v: %v: %s", args, err, out)
	}
}

func gmIdentify(t *testing.T, path string) (int, int) {
	t.Helper()
	out, err := exec.Command("gm", "identify", "-format", "%w %h", path).Output()
	if err != nil {
		t.Fatalf("gm identify %s: %v", path, err)
	}
	var w, h int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(out)), "%d %d", &w, &h); err != nil {
		t.Fatalf("parse dimensions %q: %v", string(out), err)
	}
	return w, h
}

// rgb is a sampled pixel color.
type rgb struct{ r, g, b int }

var (
	pxWhite = rgb{255, 255, 255}
	pxBlack = rgb{0, 0, 0}
	pxRed   = rgb{255, 0, 0}
)

// gmPixel samples a single pixel as (r, g, b) via a 1x1 crop + PPM
// dump. (This gm build's %[pixel:...] properties and txt: output are
// unavailable; P6 is exact for the flat-color test images.)
func gmPixel(t *testing.T, path string, x, y int) (int, int, int) {
	t.Helper()
	crop := filepath.Join(t.TempDir(), "crop.png")
	full := []string{"convert", "-crop", fmt.Sprintf("1x1+%d+%d", x, y), path, crop}
	if out, err := exec.Command("gm", full...).CombinedOutput(); err != nil {
		t.Fatalf("gm crop %s@%d,%d: %v: %s", path, x, y, err, out)
	}
	ppm := filepath.Join(t.TempDir(), "crop.ppm")
	if out, err := exec.Command("gm", "convert", crop, ppm).CombinedOutput(); err != nil {
		t.Fatalf("gm ppm %s@%d,%d: %v: %s", path, x, y, err, out)
	}
	data, err := os.ReadFile(ppm)
	if err != nil {
		t.Fatalf("read ppm: %v", err)
	}
	// P6 header: "P6\n<w> <h>\n<max>\n" followed by w*h*3 RGB bytes.
	parts := bytes.SplitN(data, []byte("\n"), 4)
	if len(parts) < 4 || string(parts[0]) != "P6" || len(parts[3]) < 3 {
		t.Fatalf("unexpected PPM content for %s@%d,%d", path, x, y)
	}
	return int(parts[3][0]), int(parts[3][1]), int(parts[3][2])
}

// checkPixel asserts a pixel's color.
func checkPixel(t *testing.T, path string, x, y int, want rgb) {
	t.Helper()
	r, g, b := gmPixel(t, path, x, y)
	got := rgb{r, g, b}
	if got != want {
		t.Errorf("pixel %s@%d,%d = %v, want %v", path, x, y, got, want)
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}

func TestGmProcessor_Crop_ExactDimensions(t *testing.T) {
	skipIfNoGm(t)
	ctx := context.Background()

	cases := []struct {
		name string
		src  []string // gm convert args for the source
	}{
		{"landscape input", []string{"-size", "1024x768", "xc:white"}},
		{"portrait input", []string{"-size", "768x1024", "xc:white"}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), "src.png")
			gmWriteTo(t, src, tt.src...)
			before := mustReadFile(t, src)

			out, err := (GmProcessor{}).Process(ctx, src, Params{320, 240, "crop", "auto"})
			if err != nil {
				t.Fatalf("Process: %v", err)
			}
			defer os.Remove(out)

			if out == src {
				t.Error("output == source; Process must write a temp output")
			}
			w, h := gmIdentify(t, out)
			if w != 320 || h != 240 {
				t.Errorf("output dimensions = %dx%d, want 320x240", w, h)
			}
			// Source is never modified.
			if !bytes.Equal(before, mustReadFile(t, src)) {
				t.Error("source file was modified by Process")
			}
		})
	}
}

func TestGmProcessor_RotateAuto(t *testing.T) {
	skipIfNoGm(t)
	ctx := context.Background()

	// Portrait source, 200x400: left half black, right half white.
	src := filepath.Join(t.TempDir(), "src.png")
	gmWriteTo(t, src, "-size", "200x400", "xc:black",
		"-fill", "white", "-draw", "rectangle 100,0 199,399")

	// rotate auto: portrait → rotate 90° (clockwise), so the black LEFT
	// half becomes the black TOP half before the center crop.
	outAuto, err := (GmProcessor{}).Process(ctx, src, Params{300, 200, "crop", "auto"})
	if err != nil {
		t.Fatalf("Process(auto): %v", err)
	}
	defer os.Remove(outAuto)
	checkPixel(t, outAuto, 150, 20, pxBlack)  // rotated: black on top
	checkPixel(t, outAuto, 150, 180, pxWhite) // rotated: white on bottom

	// rotate never: no rotation; the center crop keeps the vertical split
	// (left black, right white).
	outNever, err := (GmProcessor{}).Process(ctx, src, Params{300, 200, "crop", "never"})
	if err != nil {
		t.Fatalf("Process(never): %v", err)
	}
	defer os.Remove(outNever)
	checkPixel(t, outNever, 40, 100, pxBlack)  // unrotated: black on left
	checkPixel(t, outNever, 260, 100, pxWhite) // unrotated: white on right
}

func TestGmProcessor_Fit_Letterbox(t *testing.T) {
	skipIfNoGm(t)
	ctx := context.Background()

	// Source 400x200 solid red; canvas 300x200 (1.5:1). fit scales to
	// 300x150 and letterboxes 25px of white top+bottom.
	src := filepath.Join(t.TempDir(), "src.png")
	gmWriteTo(t, src, "-size", "400x200", "xc:red")

	out, err := (GmProcessor{}).Process(ctx, src, Params{300, 200, "fit", "never"})
	if err != nil {
		t.Fatalf("Process(fit): %v", err)
	}
	defer os.Remove(out)

	w, h := gmIdentify(t, out)
	if w != 300 || h != 200 {
		t.Fatalf("fit output = %dx%d, want 300x200", w, h)
	}
	checkPixel(t, out, 150, 10, pxWhite) // top: white letterbox
	checkPixel(t, out, 150, 100, pxRed)  // center: red content

	// Control: crop of the same source has no letterbox.
	outCrop, err := (GmProcessor{}).Process(ctx, src, Params{300, 200, "crop", "never"})
	if err != nil {
		t.Fatalf("Process(crop): %v", err)
	}
	defer os.Remove(outCrop)
	checkPixel(t, outCrop, 150, 10, pxRed) // no letterbox
}

func TestGmProcessor_GmMissing_Passthrough(t *testing.T) {
	skipIfNoGm(t)
	// Make gm unfindable for this test.
	t.Setenv("PATH", t.TempDir())
	if (GmProcessor{}).Available() {
		t.Fatal("Available() = true with empty PATH, want false")
	}

	src := filepath.Join(t.TempDir(), "src.png")
	if err := os.WriteFile(src, []byte("img"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := (GmProcessor{}).Process(context.Background(), src, Params{100, 80, "crop", "auto"})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if out != src {
		t.Errorf("Process = %q, want passthrough of %q when gm is missing", out, src)
	}
}

func TestGmProcessor_ValidationRejects(t *testing.T) {
	skipIfNoGm(t)
	src := filepath.Join(t.TempDir(), "src.png")
	if err := os.WriteFile(src, []byte("img"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := (GmProcessor{}).Process(context.Background(), src, Params{0, 80, "crop", "auto"}); err == nil {
		t.Error("Process with zero width: expected error, got none")
	}
}

// --- HTTP endpoints ---

// stubProcessor records the params it was called with and writes a
// fixed byte slice to a temp file.
type stubProcessor struct {
	name       string
	available  bool
	lastParams Params
	sawCall    bool
	outBytes   []byte
}

func (s *stubProcessor) Name() string    { return s.name }
func (s *stubProcessor) Available() bool { return s.available }
func (s *stubProcessor) Process(_ context.Context, _ string, p Params) (string, error) {
	if err := p.validate(); err != nil {
		return "", err
	}
	s.sawCall = true
	s.lastParams = p
	f, err := os.CreateTemp("", "stub-proc-*.png")
	if err != nil {
		return "", err
	}
	if _, err := f.Write(s.outBytes); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	f.Close()
	return f.Name(), nil
}

type stubElements struct {
	elems map[string]model.Element
}

func (s stubElements) GetElement(_ context.Context, id string) (model.Element, error) {
	e, ok := s.elems[id]
	if !ok {
		return model.Element{}, errors.New("element not found")
	}
	return e, nil
}

// setupPreviewMux builds an imageproc Extension with mock storage, a
// stub processor, and one element in project "proj" with an image in
// the mock store.
func setupPreviewMux(t *testing.T) (*Extension, *stubProcessor, *http.ServeMux) {
	t.Helper()
	store := storage.NewMockStorage()
	key := "projects/proj/images/e1.png"
	if err := store.PutObject(context.Background(), key,
		bytes.NewReader([]byte("FAKEPNG")), 7, "image/png"); err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	elem := model.Element{
		ID:      "e1",
		Project: "proj",
		Image:   &model.ImageInfo{ProjectLocation: "images/e1.png"},
	}
	stub := &stubProcessor{name: "stub", available: true, outBytes: []byte("PROCESSED")}
	e := New(http.NewServeMux(), Config{}, stub, store, stubElements{
		elems: map[string]model.Element{
			"e1":    elem,
			"other": {ID: "other", Project: "otherproj", Image: &model.ImageInfo{ProjectLocation: "images/other.png"}},
			"noimg": {ID: "noimg", Project: "proj"},
			"nobj":  {ID: "nobj", Project: "proj", Image: &model.ImageInfo{ProjectLocation: "images/nobj.png"}},
		},
	})
	e.RegisterRoutes(&app.App{})
	return e, stub, e.mux
}

func postPreview(t *testing.T, mux *http.ServeMux, project, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/"+project+"/ext/joleuger/imageproc/preview", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestPreviewHandler_Valid(t *testing.T) {
	_, stub, mux := setupPreviewMux(t)

	rec := postPreview(t, mux, "proj", `{"element_id":"e1","width":100,"height":80,"fit":"crop","rotate":"auto"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("Content-Type = %q, want image/png", ct)
	}
	if got := rec.Body.String(); got != "PROCESSED" {
		t.Errorf("body = %q, want the processor output %q", got, "PROCESSED")
	}
	// Params must pass through exactly as sent — never defaulted.
	want := Params{Width: 100, Height: 80, Fit: "crop", Rotate: "auto"}
	if !stub.sawCall {
		t.Fatal("processor was not called")
	}
	if stub.lastParams != want {
		t.Errorf("processor params = %+v, want %+v", stub.lastParams, want)
	}
}

func TestPreviewHandler_Validation(t *testing.T) {
	_, _, mux := setupPreviewMux(t)

	tests := []struct {
		name    string
		body    string
		wantSub string
	}{
		{"zero width", `{"element_id":"e1","height":80,"fit":"crop","rotate":"auto"}`, "width"},
		{"zero height", `{"element_id":"e1","width":100,"fit":"crop","rotate":"auto"}`, "height"},
		{"bad fit", `{"element_id":"e1","width":100,"height":80,"fit":"stretch","rotate":"auto"}`, "fit"},
		{"bad rotate", `{"element_id":"e1","width":100,"height":80,"fit":"crop","rotate":"always"}`, "rotate"},
		{"missing element_id", `{"width":100,"height":80,"fit":"crop","rotate":"auto"}`, "element_id is required"},
		{"invalid body", `{not json`, "invalid request body"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := postPreview(t, mux, "proj", tt.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tt.wantSub) {
				t.Errorf("error = %s, want it to mention %q", rec.Body.String(), tt.wantSub)
			}
		})
	}
}

func TestPreviewHandler_MissingElement(t *testing.T) {
	_, stub, mux := setupPreviewMux(t)

	rec := postPreview(t, mux, "proj", `{"element_id":"ghost","width":100,"height":80,"fit":"crop","rotate":"auto"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", rec.Code, rec.Body.String())
	}
	if stub.sawCall {
		t.Error("processor was called for a missing element")
	}
}

func TestPreviewHandler_WrongProject(t *testing.T) {
	_, _, mux := setupPreviewMux(t)

	rec := postPreview(t, mux, "proj", `{"element_id":"other","width":100,"height":80,"fit":"crop","rotate":"auto"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "does not belong") {
		t.Errorf("error = %s, want project-strict message", rec.Body.String())
	}
}

func TestPreviewHandler_NoImage(t *testing.T) {
	_, _, mux := setupPreviewMux(t)

	rec := postPreview(t, mux, "proj", `{"element_id":"noimg","width":100,"height":80,"fit":"crop","rotate":"auto"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "no image") {
		t.Errorf("error = %s, want no-image message", rec.Body.String())
	}
}

func TestPreviewHandler_MissingObject(t *testing.T) {
	_, _, mux := setupPreviewMux(t)

	rec := postPreview(t, mux, "proj", `{"element_id":"nobj","width":100,"height":80,"fit":"crop","rotate":"auto"}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "fetch element image") {
		t.Errorf("error = %s, want fetch failure message", rec.Body.String())
	}
}

func TestInfoHandler(t *testing.T) {
	_, _, mux := setupPreviewMux(t)

	req := httptest.NewRequest(http.MethodGet, "/api/proj/ext/joleuger/imageproc/info", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != `{"engine":"stub","available":true}`+"\n" {
		t.Errorf("body = %s, want stub engine info", rec.Body.String())
	}
}
