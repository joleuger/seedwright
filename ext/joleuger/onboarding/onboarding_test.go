package onboarding

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"seedwright/internal/app"
	"seedwright/internal/config"
	"seedwright/internal/server"

	"gopkg.in/yaml.v3"
)

// --- fixtures ---

func testConfig() *config.Config {
	cfg := &config.Config{
		Server:      config.ServerConfig{Listen: ":8080"},
		Database:    config.DatabaseConfig{SQLiteDatabase: "cache.db"},
		Application: config.ApplicationConfig{Title: "seedwright", DefaultProject: "default"},
		Storage:     config.StorageConfig{Type: "memory"},
	}
	cfg.SDCPP.Backends = []config.SDCPPBackend{{
		Name: "default", BaseURL: "http://127.0.0.1:1234",
	}}
	return cfg
}

func testExt(cfg *config.Config, cfgPath string) *Extension {
	c, err := LoadConfig(cfg)
	if err != nil {
		panic(err)
	}
	return &Extension{a: &app.App{Config: cfg, ConfigPath: cfgPath}, cfg: c}
}

func writeFixture(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

// --- descriptor / config ---

func extNode(t *testing.T, s string) yaml.Node {
	t.Helper()
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(s), &node); err != nil {
		t.Fatalf("unmarshal extension node: %v", err)
	}
	return node
}

func TestDescriptor_EnabledSelection(t *testing.T) {
	d := descriptor{}
	cases := []struct {
		name     string
		onboard  string
		extYAML  string
		want     bool
	}{
		{"default: no selection, no ext config", "", "", true},
		{"explicit self selection", OnboardingKey, "", true},
		{"none disables", "none", "", false},
		{"another provider wins", "some/other", "", false},
		{"enabled flag off", "", "enabled: false", false},
		{"enabled flag on + self selected", OnboardingKey, "enabled: true", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfig()
			cfg.Application.Onboarding = tc.onboard
			if tc.extYAML != "" {
				cfg.Extensions = map[string]yaml.Node{OnboardingKey: extNode(t, tc.extYAML)}
			}
			got, err := d.Enabled(cfg)
			if err != nil {
				t.Fatalf("Enabled: %v", err)
			}
			if got != tc.want {
				t.Errorf("Enabled = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestLoadConfig_Defaults(t *testing.T) {
	c, err := LoadConfig(testConfig())
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !c.Enabled {
		t.Errorf("expected enabled by default: %+v", c)
	}
	if c.AllowConfigWrite {
		t.Error("AllowConfigWrite must default to false — overwrites are opt-in")
	}
}

func TestLoadConfig_FromYAML(t *testing.T) {
	cfg := testConfig()
	cfg.Extensions = map[string]yaml.Node{
		OnboardingKey: extNode(t, "enabled: false\nallow_config_write: true"),
	}
	c, err := LoadConfig(cfg)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if c.Enabled {
		t.Error("expected enabled=false")
	}
	if !c.AllowConfigWrite {
		t.Errorf("allow_config_write: true not loaded: %+v", c)
	}
}

// --- fresh config write ---

func TestWriteConfigFile_FreshMemory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	e := testExt(testConfig(), path)

	err := e.writeConfigFile(completeRequest{
		StorageType:    "memory",
		MemoryCapacity: "20MB",
		BackendURL:     "http://127.0.0.1:1234",
		Title:          "My Box",
		ProjectName:    "default",
	})
	if err != nil {
		t.Fatalf("writeConfigFile: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	var doc struct {
		Server struct {
			Listen string `yaml:"listen"`
		} `yaml:"server"`
		SDCPP struct {
			Backends []struct {
				Name    string `yaml:"name"`
				BaseURL string `yaml:"base_url"`
			} `yaml:"backends"`
		} `yaml:"sdcpp"`
		Storage struct {
			Type     string `yaml:"type"`
			Capacity string `yaml:"capacity"`
		} `yaml:"storage"`
		Database struct {
			SQLitePath string `yaml:"sqlite_path"`
		} `yaml:"database"`
		Application struct {
			Title          string `yaml:"title"`
			DefaultProject string `yaml:"default_project"`
		} `yaml:"application"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse written config: %v\n%s", err, data)
	}
	if doc.Storage.Type != "memory" || doc.Storage.Capacity != "20MB" {
		t.Errorf("storage: %+v", doc.Storage)
	}
	if len(doc.SDCPP.Backends) != 1 || doc.SDCPP.Backends[0].BaseURL != "http://127.0.0.1:1234" {
		t.Errorf("backends: %+v", doc.SDCPP.Backends)
	}
	if doc.Application.Title != "My Box" || doc.Application.DefaultProject != "default" {
		t.Errorf("application: %+v", doc.Application)
	}
	if doc.Server.Listen != ":8080" || doc.Database.SQLitePath != "cache.db" {
		t.Errorf("carried-over sections: %+v %+v", doc.Server, doc.Database)
	}

	// Secrets file: must be 0600.
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("permissions = %v, want 0600", fi.Mode().Perm())
	}
}

// --- in-place update preserves other sections ---

const existingConfig = `
server:
  listen: ":9999"
sdcpp:
  backends:
    - name: default
      base_url: "http://old:3000"
      architecture: "sdxl"
    - name: secondary
      base_url: "http://second:3000"
storage:
  type: file
  file_path: "/old/path"
database:
  sqlite_path: "cache.db"
application:
  title: "Old Title"
  default_project: "oldproj"
auth:
  mechanism: static
  principal: "user:root"
extensions:
  joleuger/photobooth:
    enabled: false
  joleuger/console_code_authorizer:
    enabled: false
`

func TestWriteConfigFile_InPlacePreservesOtherSections(t *testing.T) {
	dir := t.TempDir()
	path := writeFixture(t, dir, "config.yaml", existingConfig)
	e := testExt(testConfig(), path)

	err := e.writeConfigFile(completeRequest{
		StorageType: "memory",
		BackendURL:  "http://127.0.0.1:1234",
		Title:       "New Title",
		// ProjectName empty — default_project must survive.
	})
	if err != nil {
		t.Fatalf("writeConfigFile: %v", err)
	}
	data, _ := os.ReadFile(path)
	s := string(data)

	var doc struct {
		Server struct {
			Listen string `yaml:"listen"`
		} `yaml:"server"`
		SDCPP struct {
			Backends []struct {
				Name    string `yaml:"name"`
				BaseURL string `yaml:"base_url"`
			} `yaml:"backends"`
		} `yaml:"sdcpp"`
		Storage struct {
			Type string `yaml:"type"`
		} `yaml:"storage"`
		Application struct {
			Title          string `yaml:"title"`
			DefaultProject string `yaml:"default_project"`
		} `yaml:"application"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse: %v\n%s", err, s)
	}

	if doc.Storage.Type != "memory" {
		t.Errorf("storage.type = %q, want memory", doc.Storage.Type)
	}
	if doc.SDCPP.Backends[0].BaseURL != "http://127.0.0.1:1234" {
		t.Errorf("backends[0].base_url = %q", doc.SDCPP.Backends[0].BaseURL)
	}
	if len(doc.SDCPP.Backends) != 2 || doc.SDCPP.Backends[1].BaseURL != "http://second:3000" {
		t.Errorf("second backend lost or changed: %+v", doc.SDCPP.Backends)
	}
	if doc.Application.Title != "New Title" {
		t.Errorf("title = %q", doc.Application.Title)
	}
	if doc.Application.DefaultProject != "oldproj" {
		t.Errorf("default_project = %q, want oldproj (untouched)", doc.Application.DefaultProject)
	}
	if doc.Server.Listen != ":9999" {
		t.Errorf("server.listen = %q, want :9999 (untouched)", doc.Server.Listen)
	}
	for _, want := range []string{
		"auth:", `principal: "user:root"`,
		"joleuger/photobooth:", "enabled: false",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("written config missing %q:\n%s", want, s)
		}
	}
}

// --- safe-write gate ---

func TestWriteGate_FreshAlwaysAllowed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml") // does not exist
	e := testExt(testConfig(), path)
	fresh, allowed, reason := e.writeGate()
	if !fresh || !allowed || reason != "" {
		t.Errorf("gate = (fresh=%v allowed=%v reason=%q), want (true, true, \"\")", fresh, allowed, reason)
	}
}

func TestWriteGate_ExistingRequiresFlag(t *testing.T) {
	dir := t.TempDir()
	path := writeFixture(t, dir, "config.yaml", "storage:\n  type: memory\n")
	e := testExt(testConfig(), path)
	fresh, allowed, reason := e.writeGate()
	if fresh || allowed {
		t.Errorf("gate = (fresh=%v allowed=%v), want (false, false)", fresh, allowed)
	}
	if !strings.Contains(reason, "allow_config_write") {
		t.Errorf("reason should point at the flag, got %q", reason)
	}

	cfg := testConfig()
	cfg.Extensions = map[string]yaml.Node{
		OnboardingKey: extNode(t, "enabled: true\nallow_config_write: true"),
	}
	e2 := testExt(cfg, path)
	fresh, allowed, reason = e2.writeGate()
	if fresh || !allowed || reason != "" {
		t.Errorf("flag-on gate = (fresh=%v allowed=%v reason=%q), want (false, true, \"\")", fresh, allowed, reason)
	}
}

// --- handler tests (complete / preview / download / profile) ---

const baseCompleteBody = `{"storage_type":"memory","memory_capacity":"20MB","backend_url":"http://127.0.0.1:1234","title":"T"}`

func postHandler(t *testing.T, e *Extension, h func(http.ResponseWriter, *http.Request), body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	w := httptest.NewRecorder()
	h(w, req)
	return w
}

func TestHandleComplete_FreshAlwaysAllowed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml") // does not exist
	e := testExt(testConfig(), path)
	w := postHandler(t, e, e.handleComplete, baseCompleteBody)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("config not written: %v", err)
	}
	var resp struct {
		OK              bool `json:"ok"`
		RestartRequired bool `json:"restart_required"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("bad JSON: %v", err)
	}
	if !resp.OK || !resp.RestartRequired {
		t.Errorf("response = %+v", resp)
	}
}

func TestHandleComplete_ExistingBlockedWithoutFlag(t *testing.T) {
	dir := t.TempDir()
	path := writeFixture(t, dir, "config.yaml", existingConfig)
	e := testExt(testConfig(), path)
	w := postHandler(t, e, e.handleComplete, baseCompleteBody)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "allow_config_write") {
		t.Errorf("error should point at the flag: %s", w.Body.String())
	}
	// The file must be untouched.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(data) != existingConfig {
		t.Error("blocked write modified the config file")
	}
}

func TestHandleComplete_ExistingRequiresConfirm(t *testing.T) {
	dir := t.TempDir()
	path := writeFixture(t, dir, "config.yaml", existingConfig)
	cfg := testConfig()
	cfg.Extensions = map[string]yaml.Node{
		OnboardingKey: extNode(t, "enabled: true\nallow_config_write: true"),
	}
	e := testExt(cfg, path)
	w := postHandler(t, e, e.handleComplete, baseCompleteBody)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "confirm_overwrite") {
		t.Errorf("error should demand confirm_overwrite: %s", w.Body.String())
	}
	data, _ := os.ReadFile(path)
	if string(data) != existingConfig {
		t.Error("unconfirmed write modified the config file")
	}
}

func TestHandleComplete_ExistingFlagOnAndConfirmed(t *testing.T) {
	dir := t.TempDir()
	path := writeFixture(t, dir, "config.yaml", existingConfig)
	cfg := testConfig()
	cfg.Extensions = map[string]yaml.Node{
		OnboardingKey: extNode(t, "enabled: true\nallow_config_write: true"),
	}
	e := testExt(cfg, path)
	body := `{"storage_type":"memory","memory_capacity":"20MB","backend_url":"http://127.0.0.1:1234","title":"New Title","confirm_overwrite":true}`
	w := postHandler(t, e, e.handleComplete, body)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	data, _ := os.ReadFile(path)
	s := string(data)
	var doc struct {
		Storage struct {
			Type string `yaml:"type"`
		} `yaml:"storage"`
		Application struct {
			Title          string `yaml:"title"`
			DefaultProject string `yaml:"default_project"`
		} `yaml:"application"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse: %v\n%s", err, s)
	}
	if doc.Storage.Type != "memory" || doc.Application.Title != "New Title" {
		t.Errorf("wizard changes missing: %+v", doc)
	}
	if doc.Application.DefaultProject != "oldproj" {
		t.Errorf("default_project = %q, want oldproj (untouched)", doc.Application.DefaultProject)
	}
	if !strings.Contains(s, `principal: "user:root"`) {
		t.Errorf("auth section lost:\n%s", s)
	}
}

func TestHandleComplete_UnknownProfile(t *testing.T) {
	e := testExt(testConfig(), filepath.Join(t.TempDir(), "config.yaml"))
	body := `{"storage_type":"memory","backend_url":"http://127.0.0.1:1234","title":"T","profile_key":"nope"}`
	w := postHandler(t, e, e.handleComplete, body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "unknown profile") {
		t.Errorf("error = %s", w.Body.String())
	}
}

func TestHandleComplete_ProfileKeyFillsTitleAndExtensions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml") // fresh
	e := testExt(testConfig(), path)
	body := `{"storage_type":"memory","memory_capacity":"20MB","backend_url":"http://127.0.0.1:1234","profile_key":"family-archive"}`
	w := postHandler(t, e, e.handleComplete, body)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("config not written: %v", err)
	}
	s := string(data)
	var doc struct {
		Application struct {
			Title string `yaml:"title"`
		} `yaml:"application"`
		Extensions map[string]struct {
			Enabled bool `yaml:"enabled"`
		} `yaml:"extensions"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse: %v\n%s", err, s)
	}
	if doc.Application.Title != "Family Photos" {
		t.Errorf("title = %q, want profile title %q", doc.Application.Title, "Family Photos")
	}
	if !doc.Extensions["joleuger/favorites"].Enabled {
		t.Errorf("profile extensions missing from fresh config:\n%s", s)
	}
}

// s3SecretConfig carries storage secrets that previews must mask.
const s3SecretConfig = `
server:
  listen: ":9999"
sdcpp:
  backends:
    - name: default
      base_url: "http://old:3000"
storage:
  type: s3
  endpoint: "http://minio:9000"
  region: "local"
  bucket: "img"
  access_key: "AKIASECRET123"
  secret_key: "hunter2hunter2"
application:
  title: "Old Title"
  default_project: "oldproj"
auth:
  mechanism: static
  principal: "user:root"
`

func TestHandlePreview_MasksSecrets(t *testing.T) {
	dir := t.TempDir()
	path := writeFixture(t, dir, "config.yaml", s3SecretConfig)
	e := testExt(testConfig(), path)
	body := `{"storage_type":"s3","endpoint":"http://minio:9000","region":"local","bucket":"img","access_key":"AKIASECRET123","secret_key":"hunter2hunter2","backend_url":"http://new:3000","title":"New"}`
	w := postHandler(t, e, e.handlePreview, body)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	var pr previewResponse
	if err := json.Unmarshal(w.Body.Bytes(), &pr); err != nil {
		t.Fatalf("bad JSON: %v", err)
	}
	if !pr.OK || !pr.ConfigExists || pr.WriteAllowed || !pr.ConfirmRequired {
		t.Errorf("preview flags: %+v", pr)
	}
	if !strings.Contains(pr.WriteBlockedReason, "allow_config_write") {
		t.Errorf("blocked reason = %q", pr.WriteBlockedReason)
	}
	if !strings.Contains(pr.YAML, maskedValue) {
		t.Errorf("secrets not masked in preview:\n%s", pr.YAML)
	}
	if strings.Contains(pr.YAML, "hunter2hunter2") || strings.Contains(pr.YAML, "AKIASECRET123") {
		t.Errorf("preview leaked real secrets:\n%s", pr.YAML)
	}
	if !strings.Contains(pr.YAML, "New") || !strings.Contains(pr.YAML, "http://new:3000") {
		t.Errorf("wizard values missing from preview:\n%s", pr.YAML)
	}
}

func TestHandlePreview_FreshAllowsWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml") // does not exist
	e := testExt(testConfig(), path)
	w := postHandler(t, e, e.handlePreview, baseCompleteBody)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	var pr previewResponse
	if err := json.Unmarshal(w.Body.Bytes(), &pr); err != nil {
		t.Fatalf("bad JSON: %v", err)
	}
	if !pr.OK || pr.ConfigExists || !pr.WriteAllowed || pr.ConfirmRequired || pr.WriteBlockedReason != "" {
		t.Errorf("fresh preview flags: %+v", pr)
	}
	if !strings.Contains(pr.YAML, "20MB") || !strings.Contains(pr.YAML, "http://127.0.0.1:1234") {
		t.Errorf("wizard values missing from preview:\n%s", pr.YAML)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("preview must not write the config file")
	}
}

func TestHandlePreview_UnknownProfile(t *testing.T) {
	e := testExt(testConfig(), filepath.Join(t.TempDir(), "config.yaml"))
	body := `{"storage_type":"memory","backend_url":"http://127.0.0.1:1234","title":"T","profile_key":"nope"}`
	w := postHandler(t, e, e.handlePreview, body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
	}
	var pr previewResponse
	if err := json.Unmarshal(w.Body.Bytes(), &pr); err != nil {
		t.Fatalf("bad JSON: %v", err)
	}
	if !strings.Contains(pr.Error, "unknown profile") {
		t.Errorf("error = %q", pr.Error)
	}
}

func TestHandleDownload_UnmaskedAttachment(t *testing.T) {
	dir := t.TempDir()
	path := writeFixture(t, dir, "config.yaml", s3SecretConfig)
	e := testExt(testConfig(), path)
	body := `{"storage_type":"s3","endpoint":"http://minio:9000","region":"local","bucket":"img","access_key":"AKIASECRET123","secret_key":"hunter2hunter2","backend_url":"http://new:3000","title":"New"}`
	w := postHandler(t, e, e.handleDownload, body)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/yaml") {
		t.Errorf("Content-Type = %q", ct)
	}
	if cd := w.Header().Get("Content-Disposition"); cd != `attachment; filename="config.yaml"` {
		t.Errorf("Content-Disposition = %q", cd)
	}
	// The download is the real, unmasked file (authz-gated at the route).
	if !strings.Contains(w.Body.String(), "hunter2hunter2") {
		t.Errorf("download must carry the real secrets:\n%s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "title: New") {
		t.Errorf("wizard values missing from download:\n%s", w.Body.String())
	}
}

func TestHandleProfile_GatedLikeOtherWrites(t *testing.T) {
	t.Run("existing without flag is blocked", func(t *testing.T) {
		dir := t.TempDir()
		path := writeFixture(t, dir, "config.yaml", existingConfig)
		e := testExt(testConfig(), path)
		w := postHandler(t, e, e.handleProfile, `{"key":"try-it"}`)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "allow_config_write") {
			t.Errorf("error = %s", w.Body.String())
		}
	})
	t.Run("unknown profile", func(t *testing.T) {
		e := testExt(testConfig(), filepath.Join(t.TempDir(), "config.yaml"))
		w := postHandler(t, e, e.handleProfile, `{"key":"nope"}`)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "unknown profile") {
			t.Errorf("error = %s", w.Body.String())
		}
	})
}

// --- profiles ---

func TestApplyProfile_TouchesOnlyListedKeys(t *testing.T) {
	dir := t.TempDir()
	path := writeFixture(t, dir, "config.yaml", existingConfig)
	e := testExt(testConfig(), path)

	p := GetProfile("family-archive")
	if p == nil {
		t.Fatal("family-archive profile missing")
	}
	if err := e.applyProfile(*p); err != nil {
		t.Fatalf("applyProfile: %v", err)
	}
	data, _ := os.ReadFile(path)
	s := string(data)

	var doc struct {
		Application struct {
			Title string `yaml:"title"`
		} `yaml:"application"`
		Storage struct {
			Type string `yaml:"type"`
		} `yaml:"storage"`
		Extensions map[string]struct {
			Enabled bool `yaml:"enabled"`
		} `yaml:"extensions"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse: %v\n%s", err, s)
	}
	if doc.Application.Title != "Family Photos" {
		t.Errorf("title = %q", doc.Application.Title)
	}
	// Storage untouched.
	if doc.Storage.Type != "file" {
		t.Errorf("storage.type = %q, want file (profiles never touch storage)", doc.Storage.Type)
	}
	// Listed keys set.
	if !doc.Extensions["joleuger/favorites"].Enabled {
		t.Error("favorites should be enabled")
	}
	if doc.Extensions["joleuger/photobooth"].Enabled {
		t.Error("photobooth should be disabled")
	}
	// Unlisted key keeps its pre-existing state.
	if doc.Extensions["joleuger/console_code_authorizer"].Enabled {
		t.Error("console_code_authorizer unlisted — must keep its existing (false) state")
	}
}

func TestApplyProfile_CreatesFileWhenMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml") // does not exist
	e := testExt(testConfig(), path)

	p := GetProfile("try-it")
	if p == nil {
		t.Fatal("try-it profile missing")
	}
	if err := e.applyProfile(*p); err != nil {
		t.Fatalf("applyProfile: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("file not created: %v", err)
	}
	var doc struct {
		Application struct {
			Title string `yaml:"title"`
		} `yaml:"application"`
		Storage struct {
			Type string `yaml:"type"`
		} `yaml:"storage"`
		Extensions map[string]struct {
			Enabled bool `yaml:"enabled"`
		} `yaml:"extensions"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse: %v\n%s", err, data)
	}
	if doc.Application.Title != "Seedwright" {
		t.Errorf("title = %q", doc.Application.Title)
	}
	// The fresh file carries the current effective config: memory storage.
	if doc.Storage.Type != "memory" {
		t.Errorf("storage.type = %q, want memory", doc.Storage.Type)
	}
	if !doc.Extensions["joleuger/batch"].Enabled || !doc.Extensions["joleuger/onboarding"].Enabled {
		t.Errorf("try-it should enable everything: %+v", doc.Extensions)
	}
}

func TestGetProfile_Unknown(t *testing.T) {
	if GetProfile("nope") != nil {
		t.Error("expected nil for unknown profile")
	}
}

// --- validation ---

func TestValidateComplete(t *testing.T) {
	valid := completeRequest{
		StorageType: "memory",
		BackendURL:  "http://127.0.0.1:1234",
		Title:       "T",
	}
	cases := []struct {
		name    string
		mutate  func(*completeRequest)
		wantErr string
	}{
		{"valid memory, no capacity", func(r *completeRequest) {}, ""},
		{"valid memory capacity", func(r *completeRequest) { r.MemoryCapacity = "20MB" }, ""},
		{"bad capacity", func(r *completeRequest) { r.MemoryCapacity = "banana" }, "memory capacity"},
		{"file without path", func(r *completeRequest) { r.StorageType = "file" }, "folder path"},
		{"file with path", func(r *completeRequest) { r.StorageType = "file"; r.FilePath = "./s" }, ""},
		{"s3 missing bucket", func(r *completeRequest) { r.StorageType = "s3"; r.Endpoint = "http://e"; r.Region = "r" }, "endpoint, region, and bucket"},
		{"s3 complete", func(r *completeRequest) {
			r.StorageType = "s3"
			r.Endpoint, r.Region, r.Bucket = "http://e", "r", "b"
		}, ""},
		{"unknown storage type", func(r *completeRequest) { r.StorageType = "redis" }, "must be memory, file, or s3"},
		{"bad backend url", func(r *completeRequest) { r.BackendURL = "not a url" }, "backend URL"},
		{"backend url without scheme", func(r *completeRequest) { r.BackendURL = "127.0.0.1:1234" }, "backend URL"},
		{"good https url", func(r *completeRequest) { r.BackendURL = "https://gpu:1234" }, ""},
		{"bad project name", func(r *completeRequest) { r.ProjectName = "has space" }, "project name"},
		{"good project name", func(r *completeRequest) { r.ProjectName = "a1._-Z" }, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := valid
			tc.mutate(&req)
			got, err := validateComplete(req)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got.Title == "T" && tc.name == "valid memory, no capacity" {
					// title stays; default only applies when empty
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not contain %q", err, tc.wantErr)
			}
		})
	}

	// Title defaulting.
	req := valid
	req.Title = ""
	got, err := validateComplete(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Title != "Seedwright" {
		t.Errorf("title default = %q, want Seedwright", got.Title)
	}
}

// --- script payload ---

func TestScriptJSON_EscapesScriptBreakers(t *testing.T) {
	p := scriptJSON(scriptPayload{
		Prefix:           "/sd",
		ConfigExists:     true,
		WriteAllowed:     false,
		ConfirmRequired:  true,
		EphemeralWarning: "warning <b>with</b> brackets",
	})
	s := string(p)
	if strings.Contains(s, "<") || strings.Contains(s, ">") {
		t.Errorf("payload must not contain raw angle brackets (script-breakers):\n%s", s)
	}
	// Must be valid JSON (the page does `const ONBOARDING = <this>`) and
	// the escapes must round-trip.
	var back map[string]any
	if err := json.Unmarshal([]byte(s), &back); err != nil {
		t.Fatalf("payload is not valid JSON: %v", err)
	}
	if back["prefix"] != "/sd" || back["writeAllowed"] != false || back["confirmRequired"] != true {
		t.Errorf("round-trip = %+v", back)
	}
	if w, _ := back["ephemeralWarning"].(string); w != "warning <b>with</b> brackets" {
		t.Errorf("ephemeralWarning round-trip = %q", w)
	}
}

// --- verify probes ---

func TestVerifyBackend(t *testing.T) {
	t.Run("reachable with model", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/sdcpp/v1/capabilities" {
				t.Errorf("path = %q", r.URL.Path)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"model":{"stem":"flux","name":"flux-schnell"}}`))
		}))
		defer srv.Close()
		ok, detail := verifyBackend(context.Background(), srv.URL)
		if !ok || detail != "model: flux-schnell" {
			t.Errorf("got ok=%v detail=%q", ok, detail)
		}
	})
	t.Run("empty url", func(t *testing.T) {
		ok, detail := verifyBackend(context.Background(), "")
		if ok || detail != "no backend configured" {
			t.Errorf("got ok=%v detail=%q", ok, detail)
		}
	})
	t.Run("unreachable", func(t *testing.T) {
		ok, detail := verifyBackend(context.Background(), "http://127.0.0.1:1")
		if ok {
			t.Errorf("expected failure, got ok: %q", detail)
		}
		if !strings.Contains(detail, "connection refused") {
			t.Errorf("detail = %q", detail)
		}
	})
}

func TestVerifyStorage(t *testing.T) {
	e := testExt(testConfig(), filepath.Join(t.TempDir(), "config.yaml"))
	ctx := context.Background()

	t.Run("memory always ok", func(t *testing.T) {
		ok, detail := e.verifyStorage(ctx, verifyRequest{
			Target: "storage", StorageType: "memory", MemoryCapacity: "1MB",
		})
		if !ok {
			t.Errorf("expected ok: %q", detail)
		}
	})
	t.Run("file with existing folder", func(t *testing.T) {
		dir := t.TempDir()
		ok, detail := e.verifyStorage(ctx, verifyRequest{
			Target: "storage", StorageType: "file", FilePath: dir,
		})
		if !ok {
			t.Errorf("expected ok: %q", detail)
		}
	})
	t.Run("s3 without endpoint fails fast", func(t *testing.T) {
		ok, detail := e.verifyStorage(ctx, verifyRequest{
			Target: "storage", StorageType: "s3", Region: "r", Bucket: "b",
		})
		if ok {
			t.Errorf("expected failure, got ok: %q", detail)
		}
		if !strings.Contains(detail, "endpoint") {
			t.Errorf("detail = %q", detail)
		}
	})
}

// --- misc ---

func TestConfigPath_Fallback(t *testing.T) {
	e := testExt(testConfig(), "")
	if e.configPath() != "config.yaml" {
		t.Errorf("configPath() = %q, want config.yaml", e.configPath())
	}
	e2 := testExt(testConfig(), "/custom/cfg.yaml")
	if e2.configPath() != "/custom/cfg.yaml" {
		t.Errorf("configPath() = %q", e2.configPath())
	}
}

func TestAtomicWrite_RefusesEmpty(t *testing.T) {
	dir := t.TempDir()
	if err := atomicWrite(filepath.Join(dir, "x.yaml"), nil); err == nil {
		t.Error("expected error for empty data")
	}
}

func TestRegisterHooks_WelcomeBannerPrefix(t *testing.T) {
	for _, tc := range []struct {
		prefix string
		want   string
	}{
		{"", `href="/onboarding"`},
		{"/sd", `href="/sd/onboarding"`},
	} {
		cfg := testConfig()
		cfg.Server.PathPrefix = tc.prefix
		e := testExt(cfg, "")
		e.pathPrefix = tc.prefix
		h := &server.Hooks{}
		e.a.Hooks = h
		e.registerHooks(e.a)
		if len(h.WelcomeExtras) != 1 {
			t.Fatalf("WelcomeExtras len = %d, want 1", len(h.WelcomeExtras))
		}
		out, err := h.WelcomeExtras[0](context.Background())
		if err != nil {
			t.Fatalf("hook error: %v", err)
		}
		if !strings.Contains(string(out), tc.want) {
			t.Errorf("hook HTML missing %q in %s", tc.want, out)
		}
	}
}
