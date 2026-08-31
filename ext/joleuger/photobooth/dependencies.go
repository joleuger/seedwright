package photobooth

import "seedwright/internal/extdep"

// Dependencies declares the photobooth's dependencies on other bundled
// extensions. Machine-checked by ext.RegisterAll (unknown keys, cycles,
// required deps enabled); must stay in sync with the Dependencies
// section of this extension's EXTENSION.md.
//
// The printer extension is an optional runtime dependency: the capture
// overlay's print controls call the printer's HTTP API from the UI (no
// Go import). When the printer is disabled, the overlay falls back to
// Retake/Keep and nothing else changes.
var Dependencies = []extdep.Dependency{
	{Key: "joleuger/printer", Kind: extdep.RuntimeOptional},
}
