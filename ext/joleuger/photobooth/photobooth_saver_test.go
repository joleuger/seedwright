package photobooth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"seedwright/internal/data"
	"seedwright/internal/data/model"
	"seedwright/internal/server"
	"seedwright/internal/storage"
)

func TestValidateSettingsFields(t *testing.T) {
	tests := []struct {
		name    string
		fields  map[string]any
		wantErr string
		want    map[string]any
	}{
		{
			name:   "empty map is a no-op",
			fields: map[string]any{},
			want:   map[string]any{},
		},
		{
			name: "all string fields",
			fields: map[string]any{
				"post_filter_prompt":          "cartoonify",
				"post_filter_reference_image": "elem-1",
				"capture_trigger_binding":     "KeyA",
				"print_printer":               "cups://localhost:631/printers/office",
			},
			want: map[string]any{
				"post_filter_prompt":          "cartoonify",
				"post_filter_reference_image": "elem-1",
				"capture_trigger_binding":     "KeyA",
				"print_printer":               "cups://localhost:631/printers/office",
			},
		},
		{
			name:   "bool fields",
			fields: map[string]any{"print_enabled": true, "keep_on_cancel": false},
			want:   map[string]any{"print_enabled": true, "keep_on_cancel": false},
		},
		{
			name:   "max_photos as JSON number",
			fields: map[string]any{"max_photos": float64(3)},
			want:   map[string]any{"max_photos": 3},
		},
		{
			name:   "max_photos as int",
			fields: map[string]any{"max_photos": 7},
			want:   map[string]any{"max_photos": 7},
		},
		{
			name:   "max_photos as string (settings page input values)",
			fields: map[string]any{"max_photos": " 4 "},
			want:   map[string]any{"max_photos": 4},
		},
		{
			name:    "max_photos below 1 clamps to 1",
			fields:  map[string]any{"max_photos": 0},
			want:    map[string]any{"max_photos": 1},
		},
		{
			name:    "max_photos above cap clamps to cap",
			fields:  map[string]any{"max_photos": 99},
			want:    map[string]any{"max_photos": maxPhotosCap},
		},
		{
			name:    "max_photos non-numeric string rejected",
			fields:  map[string]any{"max_photos": "abc"},
			wantErr: "must be an integer",
		},
		{
			name:    "max_photos fractional rejected",
			fields:  map[string]any{"max_photos": 1.5},
			wantErr: "must be an integer",
		},
		{
			name:    "string field with bool value rejected",
			fields:  map[string]any{"print_printer": true},
			wantErr: "must be a string",
		},
		{
			name:    "bool field with string value rejected",
			fields:  map[string]any{"print_enabled": "true"},
			wantErr: "must be a boolean",
		},
		{
			name:    "unknown field rejected",
			fields:  map[string]any{"no_such_field": 1},
			wantErr: "unknown settings field",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := validateSettingsFields(tt.fields)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error %q does not contain %q", err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("field %s = %v (%T), want %v", k, got[k], got[k], v)
				}
			}
			if len(got) != len(tt.want) {
				t.Errorf("got %d fields %v, want %d", len(got), got, len(tt.want))
			}
		})
	}
}

// setupSaverTest builds an extension wired to in-memory SQLite + mock
// storage with the photobooth extension columns and a project row.
func setupSaverTest(t *testing.T) (*Extension, *storage.MockStorage, *sql.DB) {
	t.Helper()
	db, err := data.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	for _, col := range []string{
		"ALTER TABLE projects ADD COLUMN ext_joleuger_photobooth_post_filter_prompt TEXT",
		"ALTER TABLE projects ADD COLUMN ext_joleuger_photobooth_post_filter_reference_image TEXT",
		"ALTER TABLE projects ADD COLUMN ext_joleuger_photobooth_trigger_binding TEXT",
	} {
		if _, err := db.Exec(col); err != nil {
			t.Fatalf("ALTER TABLE: %v", err)
		}
	}
	store := storage.NewMockStorage()
	repo := data.NewProjectRepository(db, store)
	if err := repo.CreateProject(context.Background(), model.NewProject("pb-test")); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	e := New(db, store, nil, nil, nil, Config{Enabled: true})
	return e, store, db
}

func TestSaveProjectSettings(t *testing.T) {
	e, store, db := setupSaverTest(t)
	ctx := context.Background()

	// First save creates the file at version 1 with all fields.
	err := e.saveProjectSettings(ctx, "pb-test", map[string]any{
		"post_filter_prompt":          "cartoonify",
		"capture_trigger_binding":     "KeyA",
		"print_enabled":               false,
		"print_printer":               "cups://localhost:631/printers/office",
		"max_photos":                  "7",
		"post_filter_reference_image": "",
		"keep_on_cancel":              true,
	})
	if err != nil {
		t.Fatalf("saveProjectSettings: %v", err)
	}

	raw := store.Objects()[deltaKey("pb-test")]
	if raw == nil {
		t.Fatal("delta file was not written")
	}
	var d photoboothSettings
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatalf("unmarshal delta file: %v", err)
	}
	if d.ID != "pb-test" || d.Version != 1 {
		t.Errorf("id/version = %q/%d, want pb-test/1", d.ID, d.Version)
	}
	if d.PostFilterPrompt != "cartoonify" || d.CaptureTriggerBinding != "KeyA" {
		t.Errorf("string fields not persisted: %+v", d)
	}
	if d.PrintEnabled == nil || *d.PrintEnabled != false {
		t.Errorf("print_enabled = %v, want explicit false", d.PrintEnabled)
	}
	if d.KeepOnCancel == nil || !*d.KeepOnCancel {
		t.Errorf("keep_on_cancel = %v, want true", d.KeepOnCancel)
	}
	if d.MaxPhotos != 7 || d.PrintPrinter != "cups://localhost:631/printers/office" {
		t.Errorf("max_photos/print_printer = %d/%q", d.MaxPhotos, d.PrintPrinter)
	}

	// Hot-path fields are mirrored to the projects table.
	var prompt, binding sql.NullString
	if err := db.QueryRowContext(ctx,
		`SELECT ext_joleuger_photobooth_post_filter_prompt, ext_joleuger_photobooth_trigger_binding FROM projects WHERE name = ?`,
		"pb-test").Scan(&prompt, &binding); err != nil {
		t.Fatalf("query mirror columns: %v", err)
	}
	if prompt.String != "cartoonify" || binding.String != "KeyA" {
		t.Errorf("mirror columns = %q/%q, want cartoonify/KeyA", prompt.String, binding.String)
	}

	// Second save: version bumps, unsubmitted fields survive
	// (read-modify-write), submitted fields change.
	err = e.saveProjectSettings(ctx, "pb-test", map[string]any{
		"max_photos": 3,
	})
	if err != nil {
		t.Fatalf("second saveProjectSettings: %v", err)
	}
	raw = store.Objects()[deltaKey("pb-test")]
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatalf("unmarshal delta file (2nd): %v", err)
	}
	if d.Version != 2 {
		t.Errorf("version = %d, want 2", d.Version)
	}
	if d.MaxPhotos != 3 {
		t.Errorf("max_photos = %d, want 3", d.MaxPhotos)
	}
	if d.PrintEnabled == nil || *d.PrintEnabled != false {
		t.Error("print_enabled did not survive the read-modify-write")
	}
	if d.PrintPrinter == "" || d.PostFilterPrompt != "cartoonify" {
		t.Error("unsubmitted fields were not preserved")
	}
}

func TestSaveProjectSettings_ValidationErrors(t *testing.T) {
	e, store, _ := setupSaverTest(t)
	ctx := context.Background()

	for name, fields := range map[string]map[string]any{
		"unknown field":   {"nope": "x"},
		"bad bool type":   {"print_enabled": "yes"},
		"bad int type":    {"max_photos": "many"},
		"bool for string": {"print_printer": true},
	} {
		err := e.saveProjectSettings(ctx, "pb-test", fields)
		var ve *server.ValidationError
		if !errors.As(err, &ve) {
			t.Errorf("%s: error = %v, want *server.ValidationError", name, err)
			continue
		}
		if ve.Message == "" {
			t.Errorf("%s: empty validation message", name)
		}
	}

	// Validation failures must not write the delta file.
	if raw := store.Objects()[deltaKey("pb-test")]; raw != nil {
		t.Errorf("validation failures wrote the delta file: %s", raw)
	}
}
