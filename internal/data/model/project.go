package model

import (
	"encoding/json"
	"fmt"
	"time"
)

// Project is a logical grouping of image generation elements.
type Project struct {
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

// ProjectMeta represents minimal project metadata for API responses.
type ProjectMeta struct {
	Name         string    `json:"name"`
	ID           string    `json:"id"`
	CreatedAt    time.Time `json:"created_at"`
	Hidden       bool      `json:"hidden"`
	BackendRef   string    `json:"backend_ref"`
	FriendlyName string    `json:"friendly_name"`
	PrimaryOwner string    `json:"primary_owner"`
}

// ProjectSettingsDelta represents a single extension's settings delta file.
// Delta files live at projects/{project}/ext/{owner}/{extension}/settings.json.
// Each delta file contains the extension's own {id, version, field} structure.
// Owner and extension are derived from the path and not persisted in the delta.
type ProjectSettingsDelta struct {
	ID      string         `json:"id"`
	Version int            `json:"version"`
	extFields map[string]any // extension-specific fields, populated from JSON or DB scan
}

// Field returns an extension setting value by key.
// Returns nil if the key is not set in this delta.
func (d ProjectSettingsDelta) Field(key string) any {
	if d.extFields == nil {
		return nil
	}
	return d.extFields[key]
}

// SetField stores an extension setting value by key.
// Called by the core's SettingsField populate callback or by Sync
// to populate the delta from scanned DB values.
func (d *ProjectSettingsDelta) SetField(key string, value any) {
	if d.extFields == nil {
		d.extFields = make(map[string]any)
	}
	d.extFields[key] = value
}

// Fields returns the extension settings map.
// Returns nil if no fields are set.
func (d ProjectSettingsDelta) Fields() map[string]any {
	return d.extFields
}

// MarshalJSON serializes the delta as one flat JSON object: the exported
// id/version fields plus every extension field from extFields.
// A custom marshaler is required because encoding/json cannot see the
// unexported extFields — without it, a delta written by the core's scoped
// save would silently drop all extension fields, and S3 (authoritative)
// would lose the data.
func (d ProjectSettingsDelta) MarshalJSON() ([]byte, error) {
	out := make(map[string]any, len(d.extFields)+2)
	out["id"] = d.ID
	out["version"] = d.Version
	for k, v := range d.extFields {
		out[k] = v
	}
	return json.Marshal(out)
}

// UnmarshalJSON parses a flat delta object. The "id" and "version" keys
// map to the exported fields; every other key is collected into extFields.
// Extension field names "id" and "version" are therefore reserved.
// The alias type has no methods, so the default struct decoding applies
// and the custom unmarshaler does not recurse.
func (d *ProjectSettingsDelta) UnmarshalJSON(data []byte) error {
	type alias ProjectSettingsDelta
	if err := json.Unmarshal(data, (*alias)(d)); err != nil {
		return err
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	fields := make(map[string]any, len(raw))
	for k, v := range raw {
		if k == "id" || k == "version" {
			continue
		}
		fields[k] = v
	}
	if len(fields) > 0 {
		d.extFields = fields
	}
	return nil
}

// ProjectJSON represents the full project document persisted in S3.
// It contains the same fields as ProjectSettings for consistency.
type ProjectJSON struct {
	Name          string    `json:"name"`
	SchemaVersion int       `json:"schema_version"`
	Version       int       `json:"version"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	Description   string    `json:"description,omitempty"`
	Tags          []string  `json:"tags,omitempty"`
	Hidden        bool      `json:"hidden,omitempty"`
	BackendRef    string    `json:"backend_ref,omitempty"`
	FriendlyName  string    `json:"friendly_name,omitempty"`
}

// ProjectS3Key returns the S3 key for this project's JSON document.
func (pj ProjectJSON) ProjectS3Key() string {
	return fmt.Sprintf("projects/%s/project.json", pj.Name)
}

// FromProjectJSON unmarshals a ProjectJSON from JSON.
func FromProjectJSON(data []byte) (ProjectJSON, error) {
	var pj ProjectJSON
	if err := json.Unmarshal(data, &pj); err != nil {
		return ProjectJSON{}, fmt.Errorf("unmarshal project json: %w", err)
	}
	return pj, nil
}

// ProjectSettings represents project-level metadata persisted in both
// S3 (project.json) and SQLite (projects table).
//
// SchemaVersion is the structural shape of the project.json document
// — incremented only when the JSON document structure changes.
// Version is a monotonically increasing counter for S3 sync —
// SyncFromStorage uses it to decide whether to UPDATE or INSERT
// (version-aware upsert). See DATA-MODEL.md for the version
// vs schema_version distinction.
type ProjectSettings struct {
	Name          string    `json:"name"`
	SchemaVersion int       `json:"schema_version"`
	Version       int       `json:"version"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	Description   string    `json:"description,omitempty"`
	Tags          []string  `json:"tags,omitempty"`
	Hidden        bool      `json:"hidden,omitempty"`
	BackendRef    string    `json:"backend_ref,omitempty"` // name of the selected sdcpp backend
	FriendlyName  string    `json:"friendly_name,omitempty"`
	PrimaryOwner  string    `json:"primary_owner,omitempty"`
}

// NewProject creates a new ProjectSettings with the given name (alias for NewProjectSettings).
func NewProject(name string) ProjectSettings {
	return NewProjectSettings(name)
}

// NewProjectSettings creates a new ProjectSettings with the given name.
func NewProjectSettings(name string) ProjectSettings {
	now := time.Now().UTC()
	return ProjectSettings{
		Name:          name,
		SchemaVersion: 1,
		Version:       1,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
}

// ProjectSettingsS3Key returns the S3 key for a project's settings JSON.
func (ps ProjectSettings) ProjectSettingsS3Key() string {
	return fmt.Sprintf("projects/%s/project.json", ps.Name)
}

// ProjectSettingsDeltaS3Key returns the S3 key for an extension's settings delta file.
// Delta files live at projects/{project}/ext/{owner}/{extension}/settings.json.
func ProjectSettingsDeltaS3Key(project, owner, extension string) string {
	return fmt.Sprintf("projects/%s/ext/%s/%s/settings.json", project, owner, extension)
}

// ToJSON marshals the ProjectSettings to JSON.
func (ps ProjectSettings) ToJSON() ([]byte, error) {
	return json.Marshal(ps)
}

// FromProjectSettingsJSON unmarshals a ProjectSettings from JSON.
func FromProjectSettingsJSON(data []byte) (ProjectSettings, error) {
	var ps ProjectSettings
	if err := json.Unmarshal(data, &ps); err != nil {
		return ProjectSettings{}, fmt.Errorf("unmarshal project settings: %w", err)
	}
	return ps, nil
}

// ProjectSettingsDeltaFromJSON unmarshals a delta file from JSON.
func ProjectSettingsDeltaFromJSON(data []byte) (ProjectSettingsDelta, error) {
	var d ProjectSettingsDelta
	if err := json.Unmarshal(data, &d); err != nil {
		return ProjectSettingsDelta{}, fmt.Errorf("unmarshal project settings delta: %w", err)
	}
	return d, nil
}
