package model

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
)

// Element represents a generation result (domain model).
// The JSON in S3 contains the complete generation context.
//
// Version is a monotonically increasing counter that tracks whether the
// S3 object has been modified since the last SQLite sync. On startup,
// SyncFromStorage only updates elements whose version differs from the
// cached copy. This prevents INSERT OR REPLACE (which = DELETE + INSERT
// in SQLite) from firing and triggering ON DELETE CASCADE on extension
// tables. Version is set to 1 on initial creation and incremented
// (or reset) on every modification to S3.
type Element struct {
	ID            string        `json:"id"`
	Project       string        `json:"-"`                 // derived from S3 path, not persisted in JSON
	Kind          string        `json:"kind"`              // "image"
	Origin        string        `json:"origin"`            // "generated" | "uploaded" | "ext/{owner}/{extension}"
	SchemaVersion int           `json:"schema_version"`    // structural shape of this document
	Version       int           `json:"version"`           // regeneration count for this element ID
	CreatedAt     time.Time     `json:"created_at"`
	Generation    *Generation   `json:"generation,omitempty"`  // present only for core "generated" origin
	Image         *ImageInfo    `json:"image,omitempty"`

	// extFields holds extension column values populated during ListElements.
	// Extensions register their SELECT columns via the query builder;
	// the core's populate callback stores values here keyed by column name.
	// Templates access them via a getField function, not as struct fields.
	extFields map[string]any
}

// Field returns an extension field value by column name.
// Returns nil if the column was not scanned (not registered by any extension).
func (e *Element) Field(key string) any {
	if e == nil || e.extFields == nil {
		return nil
	}
	return e.extFields[key]
}

// SetField stores an extension field value by column name.
// Called by the core's ListElements populate callback to store
// extension column values generically (no core-level knowledge of extensions).
func (e *Element) SetField(key string, value any) {
	if e.extFields == nil {
		e.extFields = make(map[string]any)
	}
	e.extFields[key] = value
}

// Generation holds everything about how a generated element came to be:
// request parameters (shape depends on Task). Only present when Element.Origin == "generated".
type Generation struct {
	Task            string        `json:"task"`                          // "txt2img" | "img2img" | ...
	Model           *ElementModel `json:"model,omitempty"`
	Prompt          string        `json:"prompt,omitempty"`
	NegativePrompt  string        `json:"negative_prompt,omitempty"`
	Width           int           `json:"width,omitempty"`
	Height          int           `json:"height,omitempty"`
	Seed            int64         `json:"seed,omitempty"`
	SampleSteps     int           `json:"sample_steps,omitempty"`
	TxtCfg          float64       `json:"txt_cfg,omitempty"`
	BackendRef      string        `json:"backend_ref,omitempty"`
	ReferenceImages []ElementRef  `json:"reference_images,omitempty"`    // task: img2img — ordered, maps to sdcpp's ref_images
	Strength        float64       `json:"strength,omitempty"`            // img2img denoise strength (0.0–1.0)
	InitImage       string        `json:"init_image,omitempty"`          // img2img base image as base64 data URL
	SourceElementID string        `json:"source_element_id,omitempty"`  // task: upscale
	UpscaleFactor   float64       `json:"upscale_factor,omitempty"`     // task: upscale
	Duration        float64       `json:"duration"`                     // job duration in seconds — set on job completion
}

// ElementRef identifies another element that this element references
// (e.g. img2img input images).
type ElementRef struct {
	ElementID string `json:"element_id"`
}

// ElementModel describes which model was used for generation.
// Only Architecture is required — other fields are optional and
// only relevant for certain model architectures (e.g. FLUX has
// variants and parameter counts; SD1.x only needs a name).
// The architecture field lets the system select which UI settings
// to show and which defaults make sense.
type ElementModel struct {
	Architecture string `json:"architecture"` // required — e.g. "flux2", "sd3", "sdxl"
	Variant      string `json:"variant"`      // optional — e.g. "klein", "dev"
	Params       string `json:"params"`       // optional — e.g. "9B", "1.0", "1.2"
	Quantization string `json:"quantization"` // optional — e.g. "Q4_K", "FP8", "FP16"
	Name         string `json:"name"`         // optional — full model filename, e.g. "flux2-dev-fp8.safetensors"
}

// ImageInfo describes the output image stored in S3.
type ImageInfo struct {
	ProjectLocation string `json:"project_location"` // project-relative path: images/{id}.png
	Format          string `json:"format"`           // "png"
	Width           int    `json:"width"`
	Height          int    `json:"height"`
	SizeBytes       int64  `json:"size_bytes"`
}

// NewImageElement creates a new Element with default values for text-to-image generation.
// modelArchitecture is the model's architecture (e.g. "flux2", "sd3").
// modelVariant, modelParams, and modelQuantization are optional model details.
// modelName is the full model filename (e.g. "flux2-dev-fp8.safetensors").
func NewImageElement(project, prompt string, width, height, sampleSteps int, cfg float64, seed int64, modelArchitecture, modelVariant, modelParams, modelQuantization, modelName string) Element {
	now := time.Now().UTC()
	return Element{
		ID:            uuid.New().String(),
		Project:       project,
		Kind:          "image",
		Origin:        "generated",
		SchemaVersion: 1,
		Version:       1,
		CreatedAt:     now,
		Generation: &Generation{
			Task:           "txt2img",
			Model:          &ElementModel{Architecture: modelArchitecture, Variant: modelVariant, Params: modelParams, Quantization: modelQuantization, Name: modelName},
			Prompt:         prompt,
			NegativePrompt: "",
			Width:          width,
			Height:         height,
			Seed:           seed,
			SampleSteps:    sampleSteps,
			TxtCfg:         cfg,
		},
	}
}

// ElementS3Key returns the S3 key where the element's JSON document is stored.
func (e Element) ElementS3Key() string {
	return fmt.Sprintf("projects/%s/elements/%s.json", e.Project, e.ID)
}

// ImageProjectLocation returns the project-relative path where the output image is stored.
func (e Element) ImageProjectLocation() string {
	return fmt.Sprintf("images/%s.png", e.ID)
}

// ToJSON marshals the Element to a JSON byte slice.
func (e Element) ToJSON() ([]byte, error) {
	return json.Marshal(e)
}

// FromJSON unmarshals a JSON byte slice into an Element.
// Expects the canonical shape with nested "generation" object.
func FromJSON(data []byte) (Element, error) {
	var e Element
	if err := json.Unmarshal(data, &e); err != nil {
		return Element{}, fmt.Errorf("unmarshal element: %w", err)
	}
	return e, nil
}

// SaveToDisk writes the element's JSON to disk for testing/inspection.
func (e Element) SaveToDisk(dir string) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := e.ToJSON()
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, e.ID+".json"), data, 0644)
}
