// Package printer implements image printing via the CUPS lp command
// as an extension to seedwright. A print button appears on the
// element detail page, opening a modal to select a printer and copies.
//
// A configured printer may declare crop: true with a dimensions canvas:
// the element image is fetched into a local file, processed onto that
// canvas by the imageproc extension (a CompileRequired dependency), and
// the processed file is passed to lp. Raw (non-crop) printers — and
// printers not present in the config — get the element image as-is.
//
// See EXTENDING.md for the extension contract.
package printer

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os/exec"
	"strconv"
	"strings"

	"seedwright/ext/joleuger/imageproc"
	"seedwright/internal/app"
	"seedwright/internal/config"
	"seedwright/internal/data/model"
	"seedwright/internal/storage"
)

// defaultCropCanvas is the print canvas used when a printer entry has
// crop: true and no dimensions configured.
const defaultCropCanvas = "1800x1200"

// Config holds Printer's tunable settings.
type Config struct {
	Enabled        bool         `yaml:"enabled"`
	Printers       []PrinterDef `yaml:"printers"`
	DefaultPrinter string       `yaml:"default_printer"`
	// Rotate is applied to crop printers: "auto" rotates portrait
	// inputs 90° before the center crop (default); "never" never
	// rotates.
	Rotate string `yaml:"rotate"`
}

// PrinterDef is a configured printer entry.
type PrinterDef struct {
	Name string `yaml:"name"`
	URI  string `yaml:"uri"`
	// Crop processes the element image onto the Dimensions canvas via
	// imageproc before printing. Raw (non-crop) entries print the element
	// image as-is.
	Crop bool `yaml:"crop"`
	// Dimensions is the print canvas as "WxH" (gm convention, e.g.
	// "1800x1200"). When crop is set and this is omitted, the default
	// canvas (defaultCropCanvas) is used.
	Dimensions string `yaml:"dimensions"`
}

// LoadConfig returns Printer's config from the global app config.
func LoadConfig(cfg *config.Config) (Config, error) {
	c := Config{Enabled: true, Rotate: "auto"}
	if err := cfg.ExtensionConfig("joleuger/printer", &c); err != nil {
		return c, fmt.Errorf("printer: config: %w", err)
	}
	switch c.Rotate {
	case "":
		c.Rotate = "auto"
	case "auto", "never":
		// valid
	default:
		return c, fmt.Errorf("printer: invalid rotate %q (valid: auto, never)", c.Rotate)
	}
	for _, p := range c.Printers {
		if p.Dimensions == "" {
			continue
		}
		if _, _, err := parseDimensions(p.Dimensions); err != nil {
			return c, fmt.Errorf("printer %q: %w", p.Name, err)
		}
	}
	return c, nil
}

// parseDimensions parses a "WxH" gm-style dimensions string into two
// positive integers.
func parseDimensions(s string) (width, height int, err error) {
	parts := strings.SplitN(s, "x", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("dimensions %q must be WxH (e.g. %s)", s, defaultCropCanvas)
	}
	w, errW := strconv.Atoi(strings.TrimSpace(parts[0]))
	h, errH := strconv.Atoi(strings.TrimSpace(parts[1]))
	if errW != nil || errH != nil || w <= 0 || h <= 0 {
		return 0, 0, fmt.Errorf("dimensions %q must be two positive integers in WxH form (e.g. %s)", s, defaultCropCanvas)
	}
	return w, h, nil
}

// parsePrinterURI parses a CUPS URI of the form cups://host:port/printers/name.
// Returns (host, port, printerName, ok). An empty host means the local CUPS
// server (localhost/127.0.0.1, with or without a port) — lp needs no -h flag.
func parsePrinterURI(uri string) (host, port, printerName string, ok bool) {
	if !strings.HasPrefix(uri, "cups://") {
		return "", "", "", false
	}
	rest := strings.TrimPrefix(uri, "cups://")
	// Split on /printers/ — everything before is host:port, after is printer name.
	idx := strings.Index(rest, "/printers/")
	if idx == -1 {
		return "", "", "", false
	}
	hostport := rest[:idx]
	printerName = rest[idx+len("/printers/"):]
	if printerName == "" {
		return "", "", "", false
	}
	if i := strings.LastIndex(hostport, ":"); i != -1 {
		host = hostport[:i]
		port = hostport[i+1:]
	} else {
		host = hostport
	}
	if host == "" || host == "localhost" || host == "127.0.0.1" {
		return "", "", printerName, true
	}
	if port == "" {
		port = "631"
	}
	return host, port, printerName, true
}

// buildLpArgs builds the lp argument list for a print job. CUPS lp takes
// the copy count as `-n N` (there is no `-#` option — it fails with
// `lp: Error - unknown option "#"`). Remote printers get `-h host:port`;
// the image file (already local) is always the last argument.
func buildLpArgs(printerURI string, copies int, file string) ([]string, error) {
	host, port, printerName, ok := parsePrinterURI(printerURI)
	if !ok {
		return nil, fmt.Errorf("invalid printer URI: %s", printerURI)
	}
	args := []string{}
	if host != "" && port != "" {
		args = append(args, "-h", host+":"+port)
	}
	args = append(args, "-d", printerName)
	if copies > 1 {
		args = append(args, "-n", strconv.Itoa(copies))
	}
	args = append(args, file)
	return args, nil
}

// elementGetter is the narrow element-read API the printer needs. The
// full data.ElementRepository satisfies it structurally; a stub keeps
// handler tests simple.
type elementGetter interface {
	GetElement(ctx context.Context, id string) (model.Element, error)
}

// Extension holds the Printer extension's state and dependencies.
type Extension struct {
	mux        *http.ServeMux
	cfg        Config
	pathPrefix string // reverse-proxy subpath from server.path_prefix
	storage    storage.StorageBackend
	elements   elementGetter
	processor  imageproc.Processor
	// runLp invokes lp with the given arguments and returns its
	// combined output. Tests inject a stub; nil uses the real binary.
	runLp func(ctx context.Context, args ...string) (string, error)
}

// New constructs a new Printer extension.
func New(mux *http.ServeMux, cfg Config) *Extension {
	return &Extension{
		mux: mux,
		cfg: cfg,
	}
}

// NewExtension constructs a Printer extension from an App instance.
// This is the entrypoint called from ext.RegisterAll.
func NewExtension(ctx context.Context, a *app.App) (*Extension, error) {
	cfg, err := LoadConfig(a.Config)
	if err != nil {
		return nil, err
	}
	processor, err := imageproc.NewProcessor(a.Config)
	if err != nil {
		return nil, err
	}
	ext := New(a.GetServeMux(), cfg)
	ext.pathPrefix = a.Config.Server.PathPrefix
	ext.storage = a.Storage
	ext.elements = a.Elements
	ext.processor = processor
	ext.RegisterHooks(a)
	ext.RegisterRoutes(a)
	return ext, nil
}

// Sync is a no-op: the printer extension has no persistent state to rebuild.
func Sync(ctx context.Context, a *app.App) error {
	return nil
}

// canvasDimensions returns the effective print canvas of an entry: the
// configured dimensions, or the default canvas for crop entries that
// configure none.
func (e *Extension) canvasDimensions(p PrinterDef) string {
	if p.Crop && p.Dimensions == "" {
		return defaultCropCanvas
	}
	return p.Dimensions
}

// cropParams returns the imageproc params for an entry, or ok=false for
// raw (non-crop) entries. Dimensions are validated in LoadConfig; a
// malformed value here (only possible with a hand-built config) degrades
// to raw printing with a log, not a failure.
func (e *Extension) cropParams(p PrinterDef) (params imageproc.Params, ok bool) {
	if !p.Crop {
		return imageproc.Params{}, false
	}
	w, h, err := parseDimensions(e.canvasDimensions(p))
	if err != nil {
		slog.Error("printer: invalid crop dimensions, printing raw", "printer", p.Name, "dimensions", p.Dimensions, "error", err)
		return imageproc.Params{}, false
	}
	return imageproc.Params{Width: w, Height: h, Fit: "crop", Rotate: e.cfg.Rotate}, true
}

// printerEntry finds a configured printer entry by URI.
func (e *Extension) printerEntry(uri string) (PrinterDef, bool) {
	for _, p := range e.cfg.Printers {
		if p.URI == uri {
			return p, true
		}
	}
	return PrinterDef{}, false
}

// listLocalPrinters runs `lpstat -p` and returns a list of discovered printers.
func listLocalPrinters() ([]PrinterInfo, error) {
	cmd := exec.Command("lpstat", "-p")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("lpstat: %w", err)
	}

	var printers []PrinterInfo
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		// Format: "printer NAME is idle.  enabled since DATE"
		// or:     "printer NAME is idle."
		// or:     "printer NAME is printing."
		if !strings.HasPrefix(line, "printer ") {
			continue
		}
		line = strings.TrimPrefix(line, "printer ")
		parts := strings.SplitN(line, " ", 2)
		if len(parts) < 1 {
			continue
		}
		name := parts[0]
		status := "unknown"

		var rest string
		if len(parts) > 1 {
			rest = parts[1]
			if strings.Contains(rest, "enabled") {
				// enabled = true (default)
			} else if strings.Contains(rest, "disabled") {
				// enabled = false
			}
			if strings.Contains(rest, "accepting") {
				// accepting = true (default)
			} else if strings.Contains(rest, "not accepting") {
				// accepting = false
			}
		}

		if rest != "" {
			if strings.Contains(strings.ToLower(rest), "printing") {
				status = "printing"
			} else if strings.Contains(strings.ToLower(rest), "idle") {
				status = "idle"
			} else if strings.Contains(strings.ToLower(rest), "stopped") {
				status = "stopped"
			}
		}

		printers = append(printers, PrinterInfo{
			Name:       name,
			Status:     status,
			Configured: false,
		})
	}

	return printers, nil
}

// PrinterInfo holds discovered printer metadata.
type PrinterInfo struct {
	Name       string `json:"name"`
	URI        string `json:"uri"`
	Configured bool   `json:"configured"`
	Status     string `json:"status"`
	// Crop and Dimensions describe the print canvas. For configured
	// crop printers these carry the effective values (the default
	// canvas applied when dimensions are omitted); they are empty for
	// raw entries and for discovered printers.
	Crop       bool   `json:"crop"`
	Dimensions string `json:"dimensions"`
}

// printersResponse is the JSON response for the printers endpoint.
type printersResponse struct {
	Printers []PrinterInfo `json:"printers"`
}

// previewResponse is the JSON response for the preview endpoint.
type previewResponse struct {
	ImageURL string `json:"image_url"`
	Filename string `json:"filename"`
}

// printRequest is the JSON body for the print endpoint.
type printRequest struct {
	ElementID  string `json:"element_id"`
	PrinterURI string `json:"printer_uri"`
	Copies     int    `json:"copies"`
}

// printResponse is the JSON response for the print endpoint.
type printResponse struct {
	JobID  string `json:"job_id"`
	Status string `json:"status"`
}

// printError is the error response for the print endpoint.
type printError struct {
	Error string `json:"error"`
}

// printJob submits a local image file to lp. The file must exist for the
// duration of the call; the caller owns its lifetime and cleanup.
func (e *Extension) printJob(ctx context.Context, jobName, file, printerURI string, copies int) (string, error) {
	_, _, printerName, ok := parsePrinterURI(printerURI)
	if !ok {
		return "", fmt.Errorf("invalid printer URI: %s", printerURI)
	}

	args, err := buildLpArgs(printerURI, copies, file)
	if err != nil {
		return "", err
	}

	var out string
	if e.runLp != nil {
		out, err = e.runLp(ctx, args...)
	} else {
		b, lpErr := exec.CommandContext(ctx, "lp", args...).CombinedOutput()
		out, err = string(b), lpErr
	}
	if err != nil {
		slog.Warn("printer: lp failed", "output", out)
		return "", fmt.Errorf("lp: %s", strings.TrimSpace(out))
	}

	// Parse job ID from output: "lp: job id is <ID>"
	output := strings.TrimSpace(out)
	if strings.HasPrefix(output, "lp: job id is ") {
		return strings.TrimPrefix(output, "lp: job id is "), nil
	}

	// Fallback: return a synthetic ID.
	if printerName != "" {
		return printerName + "-1", nil
	}
	return output, nil
}

// imagePath returns the UI path that serves an element's image:
// {prefix}/basic/{project}/element/{id}/image
// It is relative to the server root (includes the path prefix, if set);
// browsers resolve it against the page origin.
func (e *Extension) imagePath(project, elementID string) string {
	return e.pathPrefix + "/basic/" + url.PathEscape(project) + "/element/" + url.PathEscape(elementID) + "/image"
}
