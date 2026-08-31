package printer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"seedwright/ext/joleuger/imageproc"
	"seedwright/internal/app"
	"seedwright/internal/config"
	"seedwright/internal/data/model"
	"seedwright/internal/storage"
)

func TestParsePrinterURI(t *testing.T) {
	tests := []struct {
		name             string
		uri              string
		wantHost         string
		wantPort         string
		wantPrinter      string
		wantOK           bool
	}{
		{"local printer", "cups://localhost:631/printers/office", "", "", "office", true},
		{"localhost without port", "cups://localhost/printers/office", "", "", "office", true},
		{"127.0.0.1", "cups://127.0.0.1:631/printers/office", "", "", "office", true},
		{"remote with port", "cups://192.168.1.50:631/printers/kitchen", "192.168.1.50", "631", "kitchen", true},
		{"remote without port defaults 631", "cups://cups.local/printers/office", "cups.local", "631", "office", true},
		{"missing /printers/ segment", "cups://host/office", "", "", "", false},
		{"empty printer name", "cups://localhost:631/printers/", "", "", "", false},
		{"wrong scheme", "file:///var/spool/cups/office", "", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host, port, name, ok := parsePrinterURI(tt.uri)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if host != tt.wantHost || port != tt.wantPort || name != tt.wantPrinter {
				t.Errorf("got (%q, %q, %q), want (%q, %q, %q)",
					host, port, name, tt.wantHost, tt.wantPort, tt.wantPrinter)
			}
		})
	}
}

func TestImagePath(t *testing.T) {
	tests := []struct {
		name       string
		prefix     string
		project    string
		elementID  string
		want       string
	}{
		{"no prefix", "", "proj", "elem-1", "/basic/proj/element/elem-1/image"},
		{"with prefix", "/sd", "proj", "elem-1", "/sd/basic/proj/element/elem-1/image"},
		{"project escaped", "", "my proj", "elem-1", "/basic/my%20proj/element/elem-1/image"},
		{"element escaped", "/sd", "proj", "a/b", "/sd/basic/proj/element/a%2Fb/image"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &Extension{pathPrefix: tt.prefix}
			if got := e.imagePath(tt.project, tt.elementID); got != tt.want {
				t.Errorf("imagePath = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestBuildLpArgs pins the CUPS lp argument contract: copies use `-n N`
// (there is no `-#` in CUPS lp — that option fails with
// `lp: Error - unknown option "#"`), remote printers get `-h host:port`,
// and the image file is always last.
func TestBuildLpArgs(t *testing.T) {
	tests := []struct {
		name       string
		printerURI string
		copies     int
		file       string
		want       []string
		wantErr    bool
	}{
		{
			name:       "local, single copy",
			printerURI: "cups://localhost:631/printers/office",
			copies:     1,
			file:       "/tmp/a.png",
			want:       []string{"-d", "office", "/tmp/a.png"},
		},
		{
			name:       "local, two copies",
			printerURI: "cups://127.0.0.1/printers/office",
			copies:     2,
			file:       "/tmp/a.png",
			want:       []string{"-d", "office", "-n", "2", "/tmp/a.png"},
		},
		{
			name:       "local, many copies",
			printerURI: "cups://localhost/printers/dye-sub",
			copies:     99,
			file:       "/tmp/b.png",
			want:       []string{"-d", "dye-sub", "-n", "99", "/tmp/b.png"},
		},
		{
			name:       "remote, default port, single copy",
			printerURI: "cups://printserver.local/printers/a4",
			copies:     1,
			file:       "/tmp/c.png",
			want:       []string{"-h", "printserver.local:631", "-d", "a4", "/tmp/c.png"},
		},
		{
			name:       "remote, explicit port, three copies",
			printerURI: "cups://cups.office:632/printers/a4",
			copies:     3,
			file:       "/tmp/d.png",
			want:       []string{"-h", "cups.office:632", "-d", "a4", "-n", "3", "/tmp/d.png"},
		},
		{
			name:       "not a cups URI",
			printerURI: "ipp://localhost/printers/x",
			copies:     1,
			file:       "/tmp/e.png",
			wantErr:    true,
		},
		{
			name:       "missing printer name",
			printerURI: "cups://localhost:631/printers/",
			copies:     1,
			file:       "/tmp/f.png",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildLpArgs(tt.printerURI, tt.copies, tt.file)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got args %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("args = %v (%d), want %v (%d)", got, len(got), tt.want, len(tt.want))
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Fatalf("args = %v, want %v", got, tt.want)
				}
			}
			// The file must always be the last argument.
			if got[len(got)-1] != tt.file {
				t.Errorf("last arg = %q, want the file %q", got[len(got)-1], tt.file)
			}
		})
	}
}

// --- config: rotate + dimensions validation ---

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

func TestLoadConfig_RotateAndDimensions(t *testing.T) {
	tests := []struct {
		name       string
		yaml       string
		wantRotate string
		wantErr    string // substring
	}{
		{"defaults are enabled + rotate auto", "", "auto", ""},
		{"explicit rotate never", "  joleuger/printer:\n    rotate: never\n", "never", ""},
		{"invalid rotate is a config error", "  joleuger/printer:\n    rotate: always\n", "", "rotate"},
		{"crop with valid dimensions",
			"  joleuger/printer:\n" +
				"    printers:\n" +
				"      - name: dye\n" +
				"        uri: \"cups://localhost:631/printers/dye\"\n" +
				"        crop: true\n" +
				"        dimensions: \"400x300\"\n",
			"auto", ""},
		{"crop without dimensions is valid (default canvas applies)",
			"  joleuger/printer:\n" +
				"    printers:\n" +
				"      - name: dye\n" +
				"        uri: \"cups://localhost:631/printers/dye\"\n" +
				"        crop: true\n",
			"auto", ""},
		{"malformed dimensions (no x)",
			"  joleuger/printer:\n" +
				"    printers:\n" +
				"      - name: dye\n" +
				"        uri: \"cups://localhost:631/printers/dye\"\n" +
				"        dimensions: \"400300\"\n",
			"", "dimensions"},
		{"malformed dimensions (zero)",
			"  joleuger/printer:\n" +
				"    printers:\n" +
				"      - name: dye\n" +
				"        uri: \"cups://localhost:631/printers/dye\"\n" +
				"        crop: true\n" +
				"        dimensions: \"0x300\"\n",
			"", "dimensions"},
		{"malformed dimensions (non-numeric)",
			"  joleuger/printer:\n" +
				"    printers:\n" +
				"      - name: dye\n" +
				"        uri: \"cups://localhost:631/printers/dye\"\n" +
				"        dimensions: \"wide\"\n",
			"", "dimensions"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := LoadConfig(tempConfig(t, tt.yaml))
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error, got %+v", c)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %q, want it to mention %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if c.Rotate != tt.wantRotate {
				t.Errorf("rotate = %q, want %q", c.Rotate, tt.wantRotate)
			}
		})
	}
}

func TestParseDimensions(t *testing.T) {
	tests := []struct {
		s        string
		wantW    int
		wantH    int
		wantErr  bool
	}{
		{"1800x1200", 1800, 1200, false},
		{"1x1", 1, 1, false},
		{"  400 x 300 ", 400, 300, false}, // whitespace tolerated
		{"400", 0, 0, true},
		{"x300", 0, 0, true},
		{"0x300", 0, 0, true},
		{"400x0", 0, 0, true},
		{"-400x300", 0, 0, true},
		{"wide", 0, 0, true},
		{"", 0, 0, true},
	}
	for _, tt := range tests {
		t.Run("["+tt.s+"]", func(t *testing.T) {
			w, h, err := parseDimensions(tt.s)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseDimensions(%q) = %dx%d, want error", tt.s, w, h)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseDimensions(%q): %v", tt.s, err)
			}
			if w != tt.wantW || h != tt.wantH {
				t.Errorf("parseDimensions(%q) = %dx%d, want %dx%d", tt.s, w, h, tt.wantW, tt.wantH)
			}
		})
	}
}

// TestCanvasResolution pins the effective-canvas contract: crop entries
// with no configured dimensions get the default canvas; raw entries keep
// their configured value (or none).
func TestCanvasResolution(t *testing.T) {
	e := &Extension{cfg: Config{Rotate: "auto"}}

	cases := []struct {
		def  PrinterDef
		want string
	}{
		{PrinterDef{Name: "dye", Crop: true}, defaultCropCanvas},
		{PrinterDef{Name: "dye", Crop: true, Dimensions: "400x300"}, "400x300"},
		{PrinterDef{Name: "office"}, ""},
		{PrinterDef{Name: "office", Dimensions: "500x400"}, "500x400"},
	}
	for _, tt := range cases {
		t.Run(tt.def.Name, func(t *testing.T) {
			if got := e.canvasDimensions(tt.def); got != tt.want {
				t.Errorf("canvasDimensions(%+v) = %q, want %q", tt.def, got, tt.want)
			}
		})
	}
}

func TestCropParams(t *testing.T) {
	e := &Extension{cfg: Config{Rotate: "never"}}

	if _, ok := e.cropParams(PrinterDef{Name: "raw"}); ok {
		t.Error("raw entry reported as crop")
	}

	p, ok := e.cropParams(PrinterDef{Name: "dye", Crop: true, Dimensions: "400x300"})
	if !ok {
		t.Fatal("crop entry reported as raw")
	}
	if want := (imageproc.Params{Width: 400, Height: 300, Fit: "crop", Rotate: "never"}); p != want {
		t.Errorf("cropParams = %+v, want %+v", p, want)
	}

	// Omitted dimensions resolve to the default canvas.
	p, ok = e.cropParams(PrinterDef{Name: "dye", Crop: true})
	if !ok {
		t.Fatal("crop entry (default canvas) reported as raw")
	}
	if want := (imageproc.Params{Width: 1800, Height: 1200, Fit: "crop", Rotate: "never"}); p != want {
		t.Errorf("cropParams = %+v, want %+v", p, want)
	}
}

// --- HTTP endpoints ---

// stubProc records the params it was called with and returns a distinct
// processed temp file (or a configured error).
type stubProc struct {
	sawCall    bool
	lastParams imageproc.Params
	lastOut    string
	outBytes   []byte
	err        error
}

func (s *stubProc) Name() string    { return "stub" }
func (s *stubProc) Available() bool { return true }

func (s *stubProc) Process(_ context.Context, _ string, p imageproc.Params) (string, error) {
	s.sawCall = true
	s.lastParams = p
	if s.err != nil {
		return "", s.err
	}
	f, err := os.CreateTemp("", "stub-print-*.png")
	if err != nil {
		return "", err
	}
	if _, err := f.Write(s.outBytes); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	f.Close()
	s.lastOut = f.Name()
	return f.Name(), nil
}

// stubLp records lp argument lists and returns a canned output/error.
type stubLp struct {
	calls  [][]string
	output string
	err    error
}

func (s *stubLp) run(_ context.Context, args ...string) (string, error) {
	s.calls = append(s.calls, args)
	return s.output, s.err
}

type stubElems struct {
	elems map[string]model.Element
}

func (s stubElems) GetElement(_ context.Context, id string) (model.Element, error) {
	e, ok := s.elems[id]
	if !ok {
		return model.Element{}, errors.New("element not found")
	}
	return e, nil
}

// setupTestMux wires an extension with mock storage, stub elements, a
// stub image processor, and a stub lp runner — the full print path
// minus the real binaries. Configured printers:
//
//	"office"      raw (non-crop)
//	"dyesub"      crop, explicit 1800x1200 dimensions
//	"dye-default" crop, no dimensions (default canvas)
func setupTestMux(t *testing.T) (*Extension, *stubProc, *stubLp, *http.ServeMux) {
	t.Helper()
	store := storage.NewMockStorage()
	key := "projects/proj/images/e1.png"
	if err := store.PutObject(context.Background(), key,
		bytes.NewReader([]byte("RAW")), 3, "image/png"); err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	proc := &stubProc{outBytes: []byte("PROCESSED")}
	lp := &stubLp{output: "lp: job id is 42\n"}
	e := New(http.NewServeMux(), Config{
		Enabled: true,
		Rotate:  "auto",
		Printers: []PrinterDef{
			{Name: "office", URI: "cups://localhost:631/printers/office"},
			{Name: "dyesub", URI: "cups://localhost:631/printers/dyesub", Crop: true, Dimensions: "1800x1200"},
			{Name: "dye-default", URI: "cups://localhost:631/printers/dye-default", Crop: true},
		},
	})
	e.pathPrefix = "/sd"
	e.storage = store
	e.elements = stubElems{elems: map[string]model.Element{
		"e1":      {ID: "e1", Project: "proj", Image: &model.ImageInfo{ProjectLocation: "images/e1.png"}},
		"other":   {ID: "other", Project: "otherproj", Image: &model.ImageInfo{ProjectLocation: "images/other.png"}},
		"noimg":   {ID: "noimg", Project: "proj"},
		"nobj":    {ID: "nobj", Project: "proj", Image: &model.ImageInfo{ProjectLocation: "images/nobj.png"}},
	}}
	e.processor = proc
	e.runLp = lp.run
	e.RegisterRoutes(&app.App{})
	return e, proc, lp, e.mux
}

func postPrint(t *testing.T, mux *http.ServeMux, project, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/"+project+"/ext/joleuger/printer/print", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestPrintersHandler_GET(t *testing.T) {
	_, _, _, mux := setupTestMux(t)

	req := httptest.NewRequest(http.MethodGet, "/api/proj/ext/joleuger/printer/printers", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp printersResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}

	// Configured printers must be present with their effective crop
	// metadata (discovered local printers are environment-dependent, so
	// don't assert the count).
	var raw, crop, cropDefault bool
	for _, p := range resp.Printers {
		if !p.Configured {
			continue
		}
		switch p.Name {
		case "office":
			raw = true
			if p.Crop {
				t.Errorf("office: crop = true, want raw")
			}
			if p.Dimensions != "" {
				t.Errorf("office: dimensions = %q, want empty", p.Dimensions)
			}
		case "dyesub":
			crop = true
			if !p.Crop || p.Dimensions != "1800x1200" {
				t.Errorf("dyesub: crop/dimensions = %v/%q, want true/1800x1200", p.Crop, p.Dimensions)
			}
		case "dye-default":
			cropDefault = true
			if !p.Crop || p.Dimensions != defaultCropCanvas {
				t.Errorf("dye-default: crop/dimensions = %v/%q, want true/%s", p.Crop, p.Dimensions, defaultCropCanvas)
			}
		}
	}
	if !raw || !crop || !cropDefault {
		t.Errorf("configured printers missing from response (raw=%v crop=%v cropDefault=%v): %+v",
			raw, crop, cropDefault, resp.Printers)
	}
}

func TestPrintersHandler_ConfiguredFilter(t *testing.T) {
	_, _, _, mux := setupTestMux(t)

	t.Run("configured=true returns only configured printers", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/proj/ext/joleuger/printer/printers?configured=true", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("GET status = %d, want %d", rec.Code, http.StatusOK)
		}
		var resp printersResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("response is not valid JSON: %v", err)
		}
		if len(resp.Printers) != 3 {
			t.Fatalf("filtered printers = %d, want the 3 configured entries: %+v",
				len(resp.Printers), resp.Printers)
		}
		for _, p := range resp.Printers {
			if !p.Configured {
				t.Errorf("unconfigured printer %q in filtered response", p.Name)
			}
		}
	})

	t.Run("without param keeps the configured printers", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/proj/ext/joleuger/printer/printers", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("GET status = %d, want %d", rec.Code, http.StatusOK)
		}
		var resp printersResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("response is not valid JSON: %v", err)
		}
		// Discovered local printers are environment-dependent; the
		// configured ones must always be present.
		var found int
		for _, p := range resp.Printers {
			if p.Configured {
				found++
			}
		}
		if found != 3 {
			t.Errorf("configured printers in unfiltered response = %d, want 3", found)
		}
	})
}

func TestPrintersHandler_POST_IsMethodNotAllowed(t *testing.T) {
	_, _, _, mux := setupTestMux(t)

	req := httptest.NewRequest(http.MethodPost, "/api/proj/ext/joleuger/printer/printers", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestPreviewHandler_ImagePath(t *testing.T) {
	_, _, _, mux := setupTestMux(t)

	body := `{"element_id": "elem-1"}`
	req := httptest.NewRequest(http.MethodPost, "/api/myproj/ext/joleuger/printer/preview", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp previewResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}

	// Must include the project and the "element" path segment, plus the
	// configured path prefix.
	want := "/sd/basic/myproj/element/elem-1/image"
	if resp.ImageURL != want {
		t.Errorf("image_url = %q, want %q", resp.ImageURL, want)
	}
	if resp.Filename != "elem-1.png" {
		t.Errorf("filename = %q, want %q", resp.Filename, "elem-1.png")
	}
}

func TestPreviewHandler_MissingElementID(t *testing.T) {
	_, _, _, mux := setupTestMux(t)

	req := httptest.NewRequest(http.MethodPost, "/api/proj/ext/joleuger/printer/preview", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestPrintHandler_ValidationErrors(t *testing.T) {
	_, _, _, mux := setupTestMux(t)

	tests := []struct {
		name string
		body string
		want string // substring of the error message
	}{
		{"missing element_id", `{"printer_uri": "cups://localhost:631/printers/office", "copies": 1}`, "element_id is required"},
		{"missing printer_uri", `{"element_id": "e1", "copies": 1}`, "printer_uri is required"},
		// Element "e1" exists, so the invalid URI reaches printJob.
		{"invalid printer_uri", `{"element_id": "e1", "printer_uri": "not-a-cups-uri", "copies": 1}`, "invalid printer URI"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := postPrint(t, mux, "proj", tt.body)

			if rec.Code != http.StatusBadRequest && rec.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 400 or 500", rec.Code)
			}

			var errResp printError
			if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
				t.Fatalf("response is not valid JSON: %v", err)
			}
			if !strings.Contains(errResp.Error, tt.want) {
				t.Errorf("error = %q, want it to contain %q", errResp.Error, tt.want)
			}
		})
	}
}

func TestPrintHandler_CropPrinter_Processes(t *testing.T) {
	_, proc, lp, mux := setupTestMux(t)

	rec := postPrint(t, mux, "proj",
		`{"element_id": "e1", "printer_uri": "cups://localhost:631/printers/dyesub", "copies": 2}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var resp printResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if resp.JobID != "42" || resp.Status != "queued" {
		t.Errorf("response = %+v, want job 42 queued", resp)
	}

	// The processor was called with the entry's exact params.
	if !proc.sawCall {
		t.Fatal("processor was not called for a crop printer")
	}
	if want := (imageproc.Params{Width: 1800, Height: 1200, Fit: "crop", Rotate: "auto"}); proc.lastParams != want {
		t.Errorf("processor params = %+v, want %+v", proc.lastParams, want)
	}

	// lp got the PROCESSED file (not the raw source), with -n 2.
	if len(lp.calls) != 1 {
		t.Fatalf("lp calls = %d, want 1", len(lp.calls))
	}
	args := lp.calls[0]
	if len(args) < 5 || args[0] != "-d" || args[1] != "dyesub" || args[2] != "-n" || args[3] != "2" {
		t.Fatalf("lp args = %v, want -d dyesub -n 2 <file>", args)
	}
	if got := args[len(args)-1]; got != proc.lastOut {
		t.Errorf("lp file = %q, want the processed file %q", got, proc.lastOut)
	}
	// The processing artifact is cleaned up after the request.
	if _, err := os.Stat(proc.lastOut); !os.IsNotExist(err) {
		t.Errorf("processed temp file %s still exists after the request", proc.lastOut)
	}
}

func TestPrintHandler_CropDefaultCanvas(t *testing.T) {
	_, proc, _, mux := setupTestMux(t)

	rec := postPrint(t, mux, "proj",
		`{"element_id": "e1", "printer_uri": "cups://localhost:631/printers/dye-default", "copies": 1}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if !proc.sawCall {
		t.Fatal("processor was not called for a crop printer with the default canvas")
	}
	if want := (imageproc.Params{Width: 1800, Height: 1200, Fit: "crop", Rotate: "auto"}); proc.lastParams != want {
		t.Errorf("processor params = %+v, want default canvas %+v", proc.lastParams, want)
	}
}

func TestPrintHandler_RawPrinter_NoProcessing(t *testing.T) {
	_, proc, lp, mux := setupTestMux(t)

	rec := postPrint(t, mux, "proj",
		`{"element_id": "e1", "printer_uri": "cups://localhost:631/printers/office", "copies": 1}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if proc.sawCall {
		t.Error("processor was called for a raw printer")
	}
	if len(lp.calls) != 1 {
		t.Fatalf("lp calls = %d, want 1", len(lp.calls))
	}
	args := lp.calls[0]
	// The raw element image comes from the storage backend's LocalFile
	// temp file (mock storage prefix), never from a processor.
	if got := args[len(args)-1]; !strings.Contains(got, "sdcpp-mock-") {
		t.Errorf("lp file = %q, want the raw LocalFile path", got)
	}
}

func TestPrintHandler_UncfguredPrinter_PrintsRaw(t *testing.T) {
	_, proc, lp, mux := setupTestMux(t)

	rec := postPrint(t, mux, "proj",
		`{"element_id": "e1", "printer_uri": "cups://localhost:631/printers/unknown", "copies": 1}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if proc.sawCall {
		t.Error("processor was called for a printer not present in the config")
	}
	if len(lp.calls) != 1 {
		t.Fatalf("lp calls = %d, want 1", len(lp.calls))
	}
	args := lp.calls[0]
	if args[0] != "-d" || args[1] != "unknown" {
		t.Errorf("lp args = %v, want -d unknown <file>", args)
	}
}

func TestPrintHandler_WrongProject(t *testing.T) {
	_, proc, lp, mux := setupTestMux(t)

	rec := postPrint(t, mux, "proj",
		`{"element_id": "other", "printer_uri": "cups://localhost:631/printers/dyesub", "copies": 1}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "does not belong") {
		t.Errorf("error = %s, want project-strict message", rec.Body.String())
	}
	if proc.sawCall || len(lp.calls) != 0 {
		t.Error("processor or lp ran for a wrong-project element")
	}
}

func TestPrintHandler_MissingElement(t *testing.T) {
	_, proc, lp, mux := setupTestMux(t)

	rec := postPrint(t, mux, "proj",
		`{"element_id": "ghost", "printer_uri": "cups://localhost:631/printers/dyesub", "copies": 1}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "element not found") {
		t.Errorf("error = %s, want not-found message", rec.Body.String())
	}
	if proc.sawCall || len(lp.calls) != 0 {
		t.Error("processor or lp ran for a missing element")
	}
}

func TestPrintHandler_NoImage(t *testing.T) {
	_, proc, _, mux := setupTestMux(t)

	rec := postPrint(t, mux, "proj",
		`{"element_id": "noimg", "printer_uri": "cups://localhost:631/printers/dyesub", "copies": 1}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "no image") {
		t.Errorf("error = %s, want no-image message", rec.Body.String())
	}
	if proc.sawCall {
		t.Error("processor was called for an element without an image")
	}
}

func TestPrintHandler_MissingObject(t *testing.T) {
	_, proc, _, mux := setupTestMux(t)

	rec := postPrint(t, mux, "proj",
		`{"element_id": "nobj", "printer_uri": "cups://localhost:631/printers/dyesub", "copies": 1}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "fetch element image") {
		t.Errorf("error = %s, want fetch failure message", rec.Body.String())
	}
	if proc.sawCall {
		t.Error("processor was called when the object fetch failed")
	}
}

func TestPrintHandler_ProcessFailure(t *testing.T) {
	_, proc, lp, mux := setupTestMux(t)
	proc.err = errors.New("gm exploded")

	rec := postPrint(t, mux, "proj",
		`{"element_id": "e1", "printer_uri": "cups://localhost:631/printers/dyesub", "copies": 1}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "process image") {
		t.Errorf("error = %s, want process failure message", rec.Body.String())
	}
	if len(lp.calls) != 0 {
		t.Error("lp ran after a processing failure")
	}
}

// TestRenderModal_ConfiguredOnlySelection pins the dialog contract: it
// fetches the configured-only printers URL, has no lpstat-discovery
// group, and shows a hint when no printers are configured.
func TestRenderModal_ConfiguredOnlySelection(t *testing.T) {
	e := &Extension{pathPrefix: ""}
	out, err := e.renderPrintButton("proj", "e1")
	if err != nil {
		t.Fatalf("renderPrintButton: %v", err)
	}
	s := string(out)

	if !strings.Contains(s, "/ext/joleuger/printer/printers?configured=true") {
		t.Error("dialog JS does not fetch the configured-only printers URL")
	}
	if !strings.Contains(s, "No printers configured") {
		t.Error("dialog JS has no empty-list hint")
	}
	for _, absent := range []string{"!p.configured", "auto-discovered", "Local printers"} {
		if strings.Contains(s, absent) {
			t.Errorf("dialog JS still contains %q (lpstat group must be gone)", absent)
		}
	}
	// The per-element recent printer is still remembered.
	if !strings.Contains(s, "sdcpp_printer_") {
		t.Error("dialog JS lost the per-element recent-printer localStorage key")
	}
}

// TestRenderModal_CropPreviewToggle pins the D7 contract: the modal
// offers the crop preview toggle and fetches it from the imageproc
// preview endpoint.
func TestRenderModal_CropPreviewToggle(t *testing.T) {
	e := &Extension{pathPrefix: ""}
	out, err := e.renderPrintButton("proj", "e1")
	if err != nil {
		t.Fatalf("renderPrintButton: %v", err)
	}
	s := string(out)

	for _, want := range []string{
		`id="printCropPreview"`,
		"/ext/joleuger/imageproc/preview",
		"syncCropToggle",
		"updatePrintPreview",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("modal output missing %q", want)
		}
	}
}

func TestPrintHandler_LpFailure(t *testing.T) {
	_, _, lp, mux := setupTestMux(t)
	lp.err = errors.New("lp: Error - daemon not running")
	lp.output = "lp: Error - daemon not running"

	rec := postPrint(t, mux, "proj",
		`{"element_id": "e1", "printer_uri": "cups://localhost:631/printers/office", "copies": 1}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "daemon not running") {
		t.Errorf("error = %s, want the lp failure message", rec.Body.String())
	}
}
