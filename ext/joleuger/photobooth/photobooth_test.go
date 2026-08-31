package photobooth

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"seedwright/internal/data/model"
)

func TestOverlaySettingsFromDelta(t *testing.T) {
	tests := []struct {
		name             string
		fields           map[string]any
		printerAvailable bool
		wantPrint        bool
		wantMax          int
		wantKeep         bool
		wantPrinter      string
	}{
		{
			name:             "empty delta uses defaults",
			fields:           nil,
			printerAvailable: true,
			wantPrint:        true,
			wantMax:          defaultMaxPhotos,
			wantKeep:         defaultKeepOnCancel,
		},
		{
			name:             "printer extension disabled disables print",
			fields:           nil,
			printerAvailable: false,
			wantPrint:        false,
			wantMax:          defaultMaxPhotos,
			wantKeep:         defaultKeepOnCancel,
		},
		{
			name:             "print_enabled false disables print",
			fields:           map[string]any{"print_enabled": false},
			printerAvailable: true,
			wantPrint:        false,
			wantMax:          defaultMaxPhotos,
			wantKeep:         defaultKeepOnCancel,
		},
		{
			name:             "print_enabled true keeps print",
			fields:           map[string]any{"print_enabled": true},
			printerAvailable: true,
			wantPrint:        true,
			wantMax:          defaultMaxPhotos,
			wantKeep:         defaultKeepOnCancel,
		},
		{
			name:             "max_photos as JSON number",
			fields:           map[string]any{"max_photos": float64(3)},
			printerAvailable: true,
			wantPrint:        true,
			wantMax:          3,
			wantKeep:         true,
		},
		{
			name:             "max_photos as Go int",
			fields:           map[string]any{"max_photos": 8},
			printerAvailable: true,
			wantPrint:        true,
			wantMax:          8,
			wantKeep:         true,
		},
		{
			// The settings page JS submits non-checkbox inputs as strings.
			name:             "max_photos as string from settings page",
			fields:           map[string]any{"max_photos": "7"},
			printerAvailable: true,
			wantPrint:        true,
			wantMax:          7,
			wantKeep:         true,
		},
		{
			name:             "max_photos invalid string falls back to default",
			fields:           map[string]any{"max_photos": "abc"},
			printerAvailable: true,
			wantPrint:        true,
			wantMax:          defaultMaxPhotos,
			wantKeep:         true,
		},
		{
			name:             "max_photos below range clamps to 1",
			fields:           map[string]any{"max_photos": float64(0)},
			printerAvailable: true,
			wantPrint:        true,
			wantMax:          1,
			wantKeep:         true,
		},
		{
			name:             "max_photos above cap clamps to maxPhotosCap",
			fields:           map[string]any{"max_photos": float64(99)},
			printerAvailable: true,
			wantPrint:        true,
			wantMax:          maxPhotosCap,
			wantKeep:         true,
		},
		{
			name:             "keep_on_cancel false",
			fields:           map[string]any{"keep_on_cancel": false},
			printerAvailable: true,
			wantPrint:        true,
			wantMax:          defaultMaxPhotos,
			wantKeep:         false,
		},
		{
			name:             "unrelated field values are ignored",
			fields:           map[string]any{"post_filter_prompt": "cartoonify"},
			printerAvailable: true,
			wantPrint:        true,
			wantMax:          defaultMaxPhotos,
			wantKeep:         true,
		},
		{
			name:             "print_printer configured",
			fields:           map[string]any{"print_printer": "cups://localhost:631/printers/office"},
			printerAvailable: true,
			wantPrint:        true,
			wantMax:          defaultMaxPhotos,
			wantKeep:         true,
			wantPrinter:      "cups://localhost:631/printers/office",
		},
		{
			name:             "print_printer absent stays empty",
			fields:           nil,
			printerAvailable: true,
			wantPrint:        true,
			wantMax:          defaultMaxPhotos,
			wantKeep:         true,
			wantPrinter:      "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var delta model.ProjectSettingsDelta
			delta.ID = "p"
			for k, v := range tt.fields {
				delta.SetField(k, v)
			}
			got := overlaySettingsFromDelta(delta, tt.printerAvailable)
			if got.PrintAvailable != tt.wantPrint {
				t.Errorf("PrintAvailable = %v, want %v", got.PrintAvailable, tt.wantPrint)
			}
			if got.MaxPhotos != tt.wantMax {
				t.Errorf("MaxPhotos = %d, want %d", got.MaxPhotos, tt.wantMax)
			}
			if got.KeepOnCancel != tt.wantKeep {
				t.Errorf("KeepOnCancel = %v, want %v", got.KeepOnCancel, tt.wantKeep)
			}
			if got.PrinterURI != tt.wantPrinter {
				t.Errorf("PrinterURI = %q, want %q", got.PrinterURI, tt.wantPrinter)
			}
		})
	}
}

func TestPhotoboothTemplate_PrintBranch(t *testing.T) {
	var buf bytes.Buffer
	err := PhotoboothTemplate().Execute(&buf, map[string]any{
		"Title":          "Photobooth",
		"Project":        "demo",
		"prefix":         "",
		"PrintAvailable": true,
		"MaxPhotos":      3,
		"KeepOnCancel":   true,
		"PrinterURI":     "cups://localhost:631/printers/office",
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := buf.String()

	for _, want := range []string{
		`id="pbCopyCount"`,
		`id="pbPrintBtn"`,
		`id="pbPrinterHint"`,
		`#materialio_photo`,
		`#materialio_print`,
		`#materialio_cancel`,
		// html/template's JS rewriter space-pads numeric values (=  3 ;).
		`const MAX_PHOTOS =  3 ;`,
		`const KEEP_ON_CANCEL = true;`,
		// The printer is configured in project settings, not picked in the
		// overlay; html/template emits the string as a quoted JS literal.
		"const CONFIGURED_PRINTER",
		`"cups://localhost:631/printers/office"`,
		"onclick=\"pbPrint()\"",
		"onclick=\"pbCancel()\"",
		// Copy-count buttons are spaced, not touching.
		`gap: 0.4rem`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("print-branch output missing %q", want)
		}
	}
	// The fallback Keep button, the old in-overlay printer select, and
	// Retake (the ✕ is the only exit in print mode) must not render in
	// the print branch.
	for _, absent := range []string{
		`onclick="keep()"`,
		`id="pbPrinterSelect"`,
		"pbRetake",
	} {
		if strings.Contains(out, absent) {
			t.Errorf("print-branch output must not contain %q", absent)
		}
	}
}

func TestPhotoboothTemplate_FallbackBranch(t *testing.T) {
	var buf bytes.Buffer
	err := PhotoboothTemplate().Execute(&buf, map[string]any{
		"Title":          "Photobooth",
		"Project":        "demo",
		"prefix":         "",
		"PrintAvailable": false,
		"MaxPhotos":      5,
		"KeepOnCancel":   true,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := buf.String()

	for _, want := range []string{
		`class="capture-actions"`,
		`onclick="retake()"`,
		`onclick="keep()"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("fallback-branch output missing %q", want)
		}
	}
	// The print controls and their JS must not render without print.
	for _, absent := range []string{
		`id="pbCopyCount"`,
		"const MAX_PHOTOS",
		"CONFIGURED_PRINTER",
		"pbPrint()",
		"pbRetake",
	} {
		if strings.Contains(out, absent) {
			t.Errorf("fallback-branch output must not contain %q", absent)
		}
	}
}

func TestPhotoboothSettingsSection_PrinterSelect(t *testing.T) {
	e := New(nil, nil, nil, nil, nil, Config{Enabled: true})

	delta := model.ProjectSettingsDelta{}
	delta.ID = "demo"
	// Set both post-filter values so the hook's SQLite fallback is skipped
	// (this test runs without a database).
	delta.SetField("post_filter_prompt", "cartoonify")
	delta.SetField("post_filter_reference_image", "elem-1")
	delta.SetField("print_printer", "cups://localhost:631/printers/office")

	sec, err := e.SettingsSection(context.Background(), "demo", delta)
	if err != nil {
		t.Fatalf("SettingsSection: %v", err)
	}
	out := string(sec.HTML)

	for _, want := range []string{
		`id="settingsPBPrinter"`,
		`data-field="print_printer"`,
		// Saved value server-rendered as the selected option.
		`<option value="cups://localhost:631/printers/office" selected>`,
		// Printer list loaded from the printer extension's API
		// (prefix-aware, configured-only).
		`/api/demo/ext/joleuger/printer/printers?configured=true`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("settings section output missing %q", want)
		}
	}
	// A fmt.Sprintf argument-count mismatch would surface as a format
	// error marker in the rendered HTML.
	for _, bad := range []string{"%!s(MISSING)", "%!(EXTRA"} {
		if strings.Contains(out, bad) {
			t.Errorf("settings section output contains format error marker %q", bad)
		}
	}
}

func TestPhotoboothOverlay_PrintersURLFiltered(t *testing.T) {
	// The capture overlay's loadPrinters fetches the configured-only
	// printers URL (no lpstat discovery reaches the overlay).
	raw, err := photoboothFS.ReadFile("photobooth.html")
	if err != nil {
		t.Fatalf("read embedded photobooth.html: %v", err)
	}
	out := string(raw)
	if !strings.Contains(out, "/ext/joleuger/printer/printers?configured=true") {
		t.Error("overlay does not fetch the configured-only printers URL")
	}
}
