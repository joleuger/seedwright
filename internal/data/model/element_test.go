package model

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestNewImageElement_defaults(t *testing.T) {
	elem := NewImageElement("default", "a cat", 512, 512, 20, 7.0, -1, "v1-5", "", "", "", "v1-5.ckpt")

	if elem.ID == "" {
		t.Error("ID should be non-empty")
	}
	if elem.Kind != "image" {
		t.Errorf("kind = %q, want %q", elem.Kind, "image")
	}
	if elem.Generation.Model.Name != "v1-5.ckpt" {
		t.Errorf("model name = %q, want %q", elem.Generation.Model.Name, "v1-5.ckpt")
	}
	if elem.Generation.Width != 512 || elem.Generation.Height != 512 {
		t.Errorf("dimensions = %dx%d, want 512x512", elem.Generation.Width, elem.Generation.Height)
	}
	if elem.Generation.SampleSteps != 20 {
		t.Errorf("steps = %d, want 20", elem.Generation.SampleSteps)
	}
	if elem.Generation.TxtCfg != 7.0 {
		t.Errorf("cfg = %f, want 7.0", elem.Generation.TxtCfg)
	}
	if elem.Generation.Seed != -1 {
		t.Errorf("seed = %d, want -1", elem.Generation.Seed)
	}
}

func TestElement_S3Keys(t *testing.T) {
	elem := NewImageElement("test", "prompt", 512, 512, 20, 7.0, 42, "v1-5", "", "", "", "v1-5.ckpt")
	elem.ID = "abc123"

	wantElemKey := "projects/test/elements/abc123.json"
	wantImageLoc := "images/abc123.png"

	if elem.ElementS3Key() != wantElemKey {
		t.Errorf("element key = %q, want %q", elem.ElementS3Key(), wantElemKey)
	}
	if elem.ImageProjectLocation() != wantImageLoc {
		t.Errorf("image location = %q, want %q", elem.ImageProjectLocation(), wantImageLoc)
	}
}

func TestElement_JSONRoundTrip(t *testing.T) {
	original := NewImageElement("default", "a beautiful sunset", 512, 512, 20, 7.0, 42, "v1-5", "", "", "", "v1-5.ckpt")
	original.Generation.Seed = 42
	original.Generation.Model.Architecture = "v1-5"

	data, err := original.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON: %v", err)
	}

	restored, err := FromJSON(data)
	if err != nil {
		t.Fatalf("FromJSON: %v", err)
	}

	if restored.ID != original.ID {
		t.Errorf("id = %q, want %q", restored.ID, original.ID)
	}
	if restored.Generation.Prompt != original.Generation.Prompt {
		t.Errorf("prompt = %q, want %q", restored.Generation.Prompt, original.Generation.Prompt)
	}
	if restored.Generation.Seed != original.Generation.Seed {
		t.Errorf("seed = %d, want %d", restored.Generation.Seed, original.Generation.Seed)
	}
	if restored.Generation.Model.Name != original.Generation.Model.Name {
		t.Errorf("model.name = %q, want %q", restored.Generation.Model.Name, original.Generation.Model.Name)
	}
}

func TestElement_SaveAndLoadDisk(t *testing.T) {
	dir := t.TempDir()
	elem := NewImageElement("default", "test", 512, 512, 20, 7.0, 123, "v1-5", "", "", "", "v1-5.ckpt")
	elem.ID = "test-id"

	if err := elem.SaveToDisk(dir); err != nil {
		t.Fatalf("SaveToDisk: %v", err)
	}

	// Verify file exists.
	fpath := filepath.Join(dir, "test-id.json")
	data, err := os.ReadFile(fpath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	// Parse and verify.
	restored, err := FromJSON(data)
	if err != nil {
		t.Fatalf("FromJSON: %v", err)
	}
	if restored.Generation.Seed != 123 {
		t.Errorf("restored seed = %d, want 123", restored.Generation.Seed)
	}
}

func TestElement_ModelJSONMarshal(t *testing.T) {
	elem := NewImageElement("default", "prompt", 512, 512, 20, 7.0, 42, "v1-5", "", "", "", "v1-5.ckpt")
	data, err := elem.ToJSON()
	if err != nil {
		t.Fatal(err)
	}

	// Unmarshal into generic map to check JSON keys.
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}

	// Model is now nested under generation.model.
	gen, ok := m["generation"].(map[string]any)
	if !ok {
		t.Fatal("generation field missing or not an object")
	}
	model, ok := gen["model"].(map[string]any)
	if !ok {
		t.Fatal("generation.model field missing or not an object")
	}
	if model["architecture"] != "v1-5" {
		t.Errorf("generation.model.architecture = %v, want %q", model["architecture"], "v1-5")
	}
	if model["name"] != "v1-5.ckpt" {
		t.Errorf("generation.model.name = %v, want %q", model["name"], "v1-5.ckpt")
	}
}

func TestFromJSON_invalidJSON(t *testing.T) {
	_, err := FromJSON([]byte("not json"))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestElement_ProjectNotInJSON(t *testing.T) {
	elem := NewImageElement("default", "prompt", 512, 512, 20, 7.0, 42, "v1-5", "", "", "", "v1-5.ckpt")
	data, err := elem.ToJSON()
	if err != nil {
		t.Fatal(err)
	}

	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}

	if _, ok := m["project"]; ok {
		t.Error("project should not appear in element JSON")
	}
}

func TestProjectMeta_S3Key(t *testing.T) {
	pj := ProjectJSON{Name: "myproject"}
	want := "projects/myproject/project.json"
	if pj.ProjectS3Key() != want {
		t.Errorf("project key = %q, want %q", pj.ProjectS3Key(), want)
	}
}

func TestProjectMeta_JSONRoundTrip(t *testing.T) {
	original := ProjectJSON{Name: "test-project"}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}

	restored, err := FromProjectJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Name != original.Name {
		t.Errorf("name = %q, want %q", restored.Name, original.Name)
	}
}
