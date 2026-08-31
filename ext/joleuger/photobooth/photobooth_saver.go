package photobooth

// The photobooth extension owns the persistence of its own project
// settings. The core settings endpoint dispatches section saves here via
// the SettingsSavers hook (internal/server); the extension validates the
// fields, read-modify-writes its own S3 delta file (see
// photobooth_settings.go), and mirrors hot-path fields to the projects
// table (the same columns Sync populates at startup).

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	"seedwright/internal/server"
)

// settingsFieldTypes maps every accepted settings field to its kind.
// The extension owns this schema — any other key is rejected.
var settingsFieldTypes = map[string]string{
	"post_filter_prompt":          "string",
	"post_filter_reference_image": "string",
	"capture_trigger_binding":     "string",
	"print_printer":               "string",
	"print_enabled":               "bool",
	"keep_on_cancel":              "bool",
	"max_photos":                  "int",
}

// validateSettingsFields checks the fields submitted for the photobooth
// settings section and returns the accepted values (already coerced:
// max_photos is an int clamped to 1..maxPhotosCap). Unknown keys and type
// mismatches are rejected: the page submits what the section declares, so
// anything else is a client bug worth surfacing.
func validateSettingsFields(fields map[string]any) (map[string]any, error) {
	out := make(map[string]any, len(fields))
	for k, v := range fields {
		kind, known := settingsFieldTypes[k]
		if !known {
			return nil, fmt.Errorf("unknown settings field %q", k)
		}
		switch kind {
		case "string":
			s, ok := v.(string)
			if !ok {
				return nil, fmt.Errorf("%s must be a string", k)
			}
			out[k] = s
		case "bool":
			b, ok := v.(bool)
			if !ok {
				return nil, fmt.Errorf("%s must be a boolean", k)
			}
			out[k] = b
		case "int":
			n, err := toIntSetting(v)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", k, err)
			}
			if n < 1 {
				n = 1
			}
			if n > maxPhotosCap {
				n = maxPhotosCap
			}
			out[k] = n
		}
	}
	return out, nil
}

// toIntSetting accepts the JSON shapes a settings page can submit for a
// number field: a number (float64 from encoding/json, integral only), an
// int (other decoders), or a numeric string (input[type=number] values
// arrive as strings).
func toIntSetting(v any) (int, error) {
	switch n := v.(type) {
	case int:
		return n, nil
	case float64:
		if n != math.Trunc(n) {
			return 0, fmt.Errorf("must be an integer")
		}
		return int(n), nil
	case string:
		i, err := strconv.Atoi(strings.TrimSpace(n))
		if err != nil {
			return 0, fmt.Errorf("must be an integer")
		}
		return i, nil
	default:
		return 0, fmt.Errorf("must be an integer")
	}
}

// saveProjectSettings is the photobooth's SettingsSaver: it validates the
// submitted fields and persists them (S3 delta file + SQLite mirror
// columns). Invalid fields are returned as *server.ValidationError so the
// core answers 400 with the message.
func (e *Extension) saveProjectSettings(ctx context.Context, project string, fields map[string]any) error {
	validated, err := validateSettingsFields(fields)
	if err != nil {
		return &server.ValidationError{Message: err.Error()}
	}
	if len(validated) == 0 {
		return nil
	}

	// Read the current delta file (the extension's own struct; the
	// default state is "no file").
	key := deltaKey(project)
	var d photoboothSettings
	if body, _, err := e.storage.GetObject(ctx, key); err == nil && body != nil {
		raw, rErr := io.ReadAll(body)
		body.Close()
		if rErr == nil {
			if err := json.Unmarshal(raw, &d); err != nil {
				return fmt.Errorf("parse settings delta: %w", err)
			}
		}
	}

	// Apply the validated values (fields not submitted keep their values).
	d.ID = project
	for k, v := range validated {
		switch k {
		case "post_filter_prompt":
			d.PostFilterPrompt = v.(string)
		case "post_filter_reference_image":
			d.PostFilterReferenceImage = v.(string)
		case "capture_trigger_binding":
			d.CaptureTriggerBinding = v.(string)
		case "print_printer":
			d.PrintPrinter = v.(string)
		case "print_enabled":
			b := v.(bool)
			d.PrintEnabled = &b
		case "keep_on_cancel":
			b := v.(bool)
			d.KeepOnCancel = &b
		case "max_photos":
			d.MaxPhotos = v.(int)
		}
	}

	// Write the delta file.
	d.Version++
	if d.Version == 0 {
		d.Version = 1
	}
	out, err := json.Marshal(d)
	if err != nil {
		return fmt.Errorf("marshal settings delta: %w", err)
	}
	if err := e.storage.PutObject(ctx, key, strings.NewReader(string(out)), int64(len(out)), "application/json"); err != nil {
		return fmt.Errorf("write settings delta: %w", err)
	}

	// Mirror hot-path fields to the projects table (same columns Sync
	// populates at startup; the save hot path reads them from SQLite).
	if e.db != nil {
		needsMirror := false
		for k := range validated {
			if k == "post_filter_prompt" || k == "post_filter_reference_image" || k == "capture_trigger_binding" {
				needsMirror = true
				break
			}
		}
		if needsMirror {
			if _, err := e.db.ExecContext(ctx,
				`UPDATE projects SET
					ext_joleuger_photobooth_post_filter_prompt = ?,
					ext_joleuger_photobooth_post_filter_reference_image = ?,
					ext_joleuger_photobooth_trigger_binding = ?
				 WHERE name = ?`,
				d.PostFilterPrompt, d.PostFilterReferenceImage, d.CaptureTriggerBinding, project); err != nil {
				return fmt.Errorf("mirror settings to sqlite: %w", err)
			}
		}
	}

	return nil
}
