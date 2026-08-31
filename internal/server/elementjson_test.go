package server

import (
	"encoding/json"
	"testing"

	"seedwright/internal/data/model"
)

// TestElementJSONKeys verifies the JSON keys produced by json.Marshal
// for the Element struct's nested generation fields.
//
// All generation fields live under the "generation" object.
// JavaScript accessing these must use the snake_case names
// (elem.generation.prompt, elem.generation.negative_prompt, etc.).
func TestElementJSONKeys(t *testing.T) {
	elem := model.NewImageElement("test", "a cat", 512, 512, 20, 7.0, 42, "sd", "", "", "", "model.safetensors")
	elem.Generation.SampleSteps = 30
	elem.Generation.TxtCfg = 8.0
	elem.Generation.BackendRef = "default"
	elem.ID = "abc123"
	elem.Version = 1

	data, err := json.Marshal(elem)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	// Parse into map to check key casing
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	// The "generation" object must exist and contain all expected keys.
	gen, ok := raw["generation"].(map[string]interface{})
	if !ok {
		t.Fatalf("generation field missing or not an object")
	}

	// "negative_prompt" is omitted when empty (omitempty tag), so only
	// check keys that are guaranteed to be present.
	expectedKeys := []string{
		"prompt", "width", "height",
		"seed", "sample_steps", "txt_cfg", "backend_ref", "model",
	}
	for _, key := range expectedKeys {
		if _, ok := gen[key]; !ok {
			t.Errorf("generation JSON missing key %q — JS elem.generation.%s will be undefined", key, key)
		}
	}

	// Verify PascalCase keys do NOT appear in generation.
	wrongCases := []string{
		"Prompt", "NegativePrompt", "Width", "Height", "Seed",
		"SampleSteps", "TxtCfg", "BackendRef",
	}
	for _, key := range wrongCases {
		if _, ok := gen[key]; ok {
			t.Errorf("generation JSON has unexpected key %q — use snake_case", key)
		}
	}
}

// TestReuseSettingsJSPropertyNames ensures the JS reuseSettings()
// function uses the correct snake_case property names to read
// from the elem.generation JSON object.
func TestReuseSettingsJSPropertyNames(t *testing.T) {
	elem := model.NewImageElement("test", "a cat in a field", 768, 512, 25, 9.0, 12345, "sd", "", "", "", "model.safetensors")
	elem.ID = "abc123"
	data, _ := json.Marshal(elem)
	var raw map[string]interface{}
	json.Unmarshal(data, &raw)

	gen, ok := raw["generation"].(map[string]interface{})
	if !ok {
		t.Fatalf("generation field missing")
	}

	// Simulate what reuseSettings() does in JS.
	// These are the CORRECT snake_case property names under generation.
	// "negative_prompt" is omitted when empty (omitempty tag), so exclude it.
	props := []string{
		"prompt", "width", "height",
		"sample_steps", "txt_cfg", "seed",
	}
	for _, p := range props {
		v, ok := gen[p]
		if !ok {
			t.Errorf("JS elem.generation.%s returned nil — property name mismatch", p)
			continue
		}
		if v == nil {
			t.Errorf("JS elem.generation.%s is nil", p)
		}
	}
}
