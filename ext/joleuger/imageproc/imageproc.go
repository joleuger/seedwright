// Package imageproc is a pure capability provider for image processing.
// It owns one thing: the processing engine (currently gm, plus a
// passthrough "none"). It exposes the engine to in-process consumers
// (the printer) via the Processor API and to the UI via HTTP endpoints.
//
// It has NO processing defaults of any kind — no default dimensions,
// fit, or rotation. Every parameter is supplied by the caller (e.g. the
// per-printer crop configuration); zero or invalid values are rejected,
// not defaulted.
//
// See EXTENDING.md for the extension contract.
package imageproc

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"

	"seedwright/internal/config"
)

// Config holds imageproc's tunable settings: engine selection only.
type Config struct {
	Enabled bool   `yaml:"enabled"`
	Engine  string `yaml:"engine"` // "gm" (default) | "none" (passthrough)
}

// LoadConfig returns imageproc's config from the global app config.
// An empty engine defaults to "gm" (a capability-availability default,
// not a processing default); any other unknown value is a config error.
func LoadConfig(cfg *config.Config) (Config, error) {
	c := Config{Enabled: true, Engine: "gm"}
	if err := cfg.ExtensionConfig("joleuger/imageproc", &c); err != nil {
		return c, fmt.Errorf("imageproc: config: %w", err)
	}
	switch c.Engine {
	case "":
		c.Engine = "gm"
	case "gm", "none":
		// valid
	default:
		return c, fmt.Errorf("imageproc: unknown engine %q (valid: gm, none)", c.Engine)
	}
	return c, nil
}

// Params — all fields required; zero or invalid values are rejected,
// not defaulted.
type Params struct {
	Width  int    // > 0
	Height int    // > 0
	Fit    string // "crop" | "fit"
	Rotate string // "auto" | "never"
}

// validate rejects zero/invalid values. imageproc has no defaults: a
// caller that omits a parameter gets an error, not a guess.
func (p Params) validate() error {
	if p.Width <= 0 {
		return fmt.Errorf("imageproc: width must be > 0 (got %d); there are no defaults", p.Width)
	}
	if p.Height <= 0 {
		return fmt.Errorf("imageproc: height must be > 0 (got %d); there are no defaults", p.Height)
	}
	if p.Fit != "crop" && p.Fit != "fit" {
		return fmt.Errorf("imageproc: fit must be \"crop\" or \"fit\" (got %q); there are no defaults", p.Fit)
	}
	if p.Rotate != "auto" && p.Rotate != "never" {
		return fmt.Errorf("imageproc: rotate must be \"auto\" or \"never\" (got %q); there are no defaults", p.Rotate)
	}
	return nil
}

// Processor is the in-process image-processing API.
type Processor interface {
	// Name returns the engine name ("gm", "none").
	Name() string
	// Available reports whether the engine binary is on PATH (gm).
	Available() bool
	// Process writes a temp output (source never modified) and returns
	// its path; the caller removes it. Passthrough (returns srcPath,
	// nil) happens only for engine "none" or gm absent — never for
	// bad params.
	Process(ctx context.Context, srcPath string, p Params) (string, error)
}

// NewProcessor builds the configured Processor. It reads imageproc's
// own config section (the engine) only.
func NewProcessor(cfg *config.Config) (Processor, error) {
	c, err := LoadConfig(cfg)
	if err != nil {
		return nil, err
	}
	if c.Engine == "none" {
		return noneProcessor{}, nil
	}
	return GmProcessor{}, nil
}

// noneProcessor is the passthrough engine: no processing. Params are
// still validated — bad params are never passed through.
type noneProcessor struct{}

func (noneProcessor) Name() string    { return "none" }
func (noneProcessor) Available() bool { return true }

func (noneProcessor) Process(_ context.Context, srcPath string, p Params) (string, error) {
	if err := p.validate(); err != nil {
		return "", err
	}
	return srcPath, nil
}

// GmProcessor is the gm (GraphicsMagick) engine.
type GmProcessor struct{}

func (GmProcessor) Name() string { return "gm" }

// Available reports whether the gm binary is on PATH.
func (GmProcessor) Available() bool {
	_, err := exec.LookPath("gm")
	return err == nil
}

func (GmProcessor) Process(ctx context.Context, srcPath string, p Params) (string, error) {
	if err := p.validate(); err != nil {
		return "", err
	}
	if !(GmProcessor{}).Available() {
		slog.Warn("imageproc: gm not found on PATH, passing source through unmodified")
		return srcPath, nil
	}

	// rotate auto: rotate 90° iff the input is portrait.
	rotate90 := false
	if p.Rotate == "auto" {
		w, h, err := gmDimensions(ctx, srcPath)
		if err != nil {
			return "", fmt.Errorf("imageproc: identify %s: %w", srcPath, err)
		}
		rotate90 = h > w
	}

	size := fmt.Sprintf("%dx%d", p.Width, p.Height)
	args := []string{"convert", srcPath, "-filter", "Lanczos"}
	if rotate90 {
		args = append(args, "-rotate", "90")
	}
	if p.Fit == "crop" {
		args = append(args, "-resize", size+"^", "-gravity", "center", "-extent", size)
	} else { // fit: letterbox onto a white canvas
		args = append(args, "-resize", size, "-background", "white", "-gravity", "center", "-extent", size)
	}

	outFile, err := os.CreateTemp("", "sdcpp-imageproc-*.png")
	if err != nil {
		return "", fmt.Errorf("imageproc: create temp output: %w", err)
	}
	outName := outFile.Name()
	if err := outFile.Close(); err != nil {
		os.Remove(outName)
		return "", fmt.Errorf("imageproc: close temp output: %w", err)
	}
	args = append(args, outName)

	out, err := exec.CommandContext(ctx, "gm", args...).CombinedOutput()
	if err != nil {
		os.Remove(outName)
		return "", fmt.Errorf("imageproc: gm convert: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return outName, nil
}

// gmDimensions returns the source image's (width, height) via
// `gm identify -format "%w %h"`.
func gmDimensions(ctx context.Context, path string) (int, int, error) {
	out, err := exec.CommandContext(ctx, "gm", "identify", "-format", "%w %h", path).Output()
	if err != nil {
		return 0, 0, err
	}
	var w, h int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(out)), "%d %d", &w, &h); err != nil {
		return 0, 0, fmt.Errorf("imageproc: parse dimensions %q: %w", string(out), err)
	}
	return w, h, nil
}
