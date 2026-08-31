package photobooth

// photoboothSettings represents the S3 delta file for a project's
// photobooth settings.  Files are created lazily on first set; default
// state (no file) means everything is unset.
//
// Files live at projects/{project}/ext/joleuger/photobooth/settings.json.
type photoboothSettings struct {
	ID                       string `json:"id"`
	Version                  int    `json:"version"`
	PostFilterPrompt         string `json:"post_filter_prompt"`
	PostFilterReferenceImage string `json:"post_filter_reference_image"`
	CaptureTriggerBinding    string `json:"capture_trigger_binding"`
	// Capture-overlay print settings (photobooth 2.0). Omitted when unset;
	// unset means the built-in defaults (print on, keep on cancel, 5 copies).
	// PrintEnabled/KeepOnCancel are pointers so an explicit false survives
	// the extension's own read-modify-write of this file (a plain bool
	// would be indistinguishable from unset after a round-trip).
	PrintEnabled  *bool `json:"print_enabled,omitempty"`
	KeepOnCancel  *bool `json:"keep_on_cancel,omitempty"`
	MaxPhotos     int   `json:"max_photos,omitempty"`
	// PrintPrinter is the CUPS URI of the printer used in the capture
	// preview. Empty (omitted) means "no printer configured" — the
	// built-in default; no pointer needed because empty is the unset state.
	PrintPrinter string `json:"print_printer,omitempty"`
}
