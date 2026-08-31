package onboarding

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"seedwright/internal/storage"
)

// State is the live situation the Customize page renders against.
type State struct {
	ConfigFileExists bool
	ConfigPath       string

	// AllowConfigWrite mirrors the running config's
	// extensions.joleuger/onboarding.allow_config_write flag;
	// ConfigWriteDetail is the user-facing status-card line.
	AllowConfigWrite  bool
	ConfigWriteDetail string
	// EphemeralWarning is non-empty when the config path sits on
	// ephemeral storage (Linux: tmpfs/overlayfs) — writing there may
	// not survive a restart.
	EphemeralWarning string

	StorageType   string
	StorageOK     bool
	StorageDetail string
	MemoryUsed    int64
	MemoryCap     int64

	BackendName string
	BackendURL  string
	BackendArch string
	BackendOK   bool
	BackendDetail string

	Title       string
	ProjectName string

	Profiles []Profile
}

// buildState probes the live app: config file presence, storage
// reachability, and sdcpp capabilities.
func (e *Extension) buildState(ctx context.Context) State {
	cfg := e.a.Config
	s := State{
		ConfigPath:  e.a.ConfigPath,
		StorageType: cfg.Storage.Type,
		BackendName: cfg.DefaultBackend(),
		BackendArch: cfg.BackendArchitecture(cfg.DefaultBackend()),
		Title:       cfg.Application.Title,
		ProjectName: cfg.Application.DefaultProject,
		Profiles:    Profiles,
	}
	if len(cfg.SDCPP.Backends) > 0 {
		s.BackendURL = cfg.SDCPP.Backends[0].BaseURL
	}
	if s.ConfigPath != "" {
		if _, err := os.Stat(s.ConfigPath); err == nil {
			s.ConfigFileExists = true
		}
	}

	// Config write gate + ephemeral-storage warning.
	fresh, allowed, reason := e.writeGate()
	s.AllowConfigWrite = e.cfg.AllowConfigWrite
	switch {
	case fresh:
		s.ConfigWriteDetail = "fresh — the first write is always allowed (nothing to overwrite)"
	case allowed:
		s.ConfigWriteDetail = "allowed — the wizard asks for confirmation before overwriting"
	default:
		s.ConfigWriteDetail = reason
	}
	s.EphemeralWarning = ephemeralStorageWarning(e.configPath())

	// Storage reachability probe.
	if _, err := e.a.Storage.ListObjects(ctx, "projects/"); err != nil {
		s.StorageDetail = "unreachable: " + err.Error()
	} else {
		s.StorageOK = true
		switch s.StorageType {
		case "memory":
			if mem, ok := e.a.Storage.(*storage.MemoryStorage); ok {
				s.MemoryUsed, s.MemoryCap = mem.Used()
			}
			s.StorageDetail = fmt.Sprintf("ephemeral memory — %s of %s used, lost on restart",
				humanBytes(s.MemoryUsed), humanBytes(s.MemoryCap))
		case "file":
			s.StorageDetail = "local folder " + cfg.Storage.FilePath
		case "s3":
			s.StorageDetail = fmt.Sprintf("S3 bucket %q @ %s", cfg.Storage.Bucket, cfg.Storage.Endpoint)
		}
	}

	s.BackendOK, s.BackendDetail = verifyBackend(ctx, s.BackendURL)
	return s
}

// verifyBackend pings the sdcpp capabilities endpoint and reports the
// model name when reachable.
func verifyBackend(ctx context.Context, baseURL string) (bool, string) {
	base := strings.TrimRight(baseURL, "/")
	if base == "" {
		return false, "no backend configured"
	}
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/sdcpp/v1/capabilities", nil)
	if err != nil {
		return false, err.Error()
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, humanizeConnErr(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Sprintf("sdcpp answered HTTP %d", resp.StatusCode)
	}
	var caps struct {
		Model *struct {
			Stem string `json:"stem"`
			Name string `json:"name"`
		} `json:"model"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&caps); err != nil {
		return true, "reachable (unparseable capabilities)"
	}
	name := ""
	if caps.Model != nil {
		name = caps.Model.Name
		if name == "" {
			name = caps.Model.Stem
		}
	}
	if name != "" {
		return true, "model: " + name
	}
	return true, "reachable"
}

// humanizeConnErr shortens a Go net error to something a first-time
// user can act on.
func humanizeConnErr(err error) string {
	msg := err.Error()
	if strings.Contains(msg, "connection refused") {
		return "connection refused — is sdcpp running?"
	}
	if i := strings.Index(msg, "dial tcp "); i >= 0 {
		return msg[i+len("dial tcp "):]
	}
	return msg
}

// humanBytes formats a byte count for display.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
