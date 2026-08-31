package onboarding

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// completeRequest is the wizard's Write/Preview/Download payload.
type completeRequest struct {
	StorageType    string `json:"storage_type"` // "memory" | "file" | "s3"
	MemoryCapacity string `json:"memory_capacity"`
	FilePath       string `json:"file_path"`
	Endpoint       string `json:"endpoint"`
	Region         string `json:"region"`
	Bucket         string `json:"bucket"`
	AccessKey      string `json:"access_key"`
	SecretKey      string `json:"secret_key"`
	ForcePathStyle bool   `json:"force_path_style"`

	BackendURL  string `json:"backend_url"`
	BackendArch string `json:"backend_architecture"`

	Title       string `json:"title"`
	ProjectName string `json:"project_name"`

	// ProfileKey applies a profile's extension flags (and title, when the
	// title field is empty) as part of this write. "" = no profile.
	ProfileKey string `json:"profile_key"`
	// ConfirmOverwrite must be true when overwriting an existing config
	// file (the UI's checkbox). Fresh writes never need it.
	ConfirmOverwrite bool `json:"confirm_overwrite"`
}

// configPath returns where config.yaml is written back (the path the
// app was started with, or the conventional default).
func (e *Extension) configPath() string {
	if e.a.ConfigPath != "" {
		return e.a.ConfigPath
	}
	return "config.yaml"
}

// writeGate reports what a config write may do right now: fresh (no file
// present — always allowed, there is nothing to overwrite) or an
// overwrite that needs the running config's allow_config_write flag.
// reason explains a blocked overwrite in user-facing words.
func (e *Extension) writeGate() (fresh, allowed bool, reason string) {
	if _, err := os.Stat(e.configPath()); os.IsNotExist(err) {
		return true, true, ""
	}
	if e.cfg.AllowConfigWrite {
		return false, true, ""
	}
	return false, false, "overwriting " + filepath.Base(e.configPath()) + " is disabled in the running config — set extensions.joleuger/onboarding.allow_config_write: true and restart to let the wizard write it"
}

// writeConfigFile persists the wizard's choices to the config file.
// When the file already exists it is updated in place (YAML node
// surgery) so sections the wizard does not know about — auth,
// extensions, extra backends — survive untouched. When it does not
// exist, a fresh minimal document is written. The caller is
// responsible for the writeGate check; this function only writes.
func (e *Extension) writeConfigFile(req completeRequest) error {
	path := e.configPath()
	if _, err := os.Stat(path); err == nil {
		return e.updateConfigFile(path, req)
	}
	return e.writeFreshConfig(path, req)
}

// updateConfigFile applies the wizard's choices to an existing config
// file, preserving everything else.
func (e *Extension) updateConfigFile(path string, req completeRequest) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parse existing config: %w", err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		// Empty or malformed document — fall back to a fresh file.
		return e.writeFreshConfig(path, req)
	}
	root := doc.Content[0]

	// The profile's extension flags go in first so the wizard's own
	// fields (title, storage, backend) win where they overlap.
	if req.ProfileKey != "" {
		if err := applyProfileFlags(root, req.ProfileKey); err != nil {
			return err
		}
	}
	applyWizardChanges(root, req)

	out := &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{root}}
	return atomicWrite(path, marshalYAML(out))
}

// applyWizardChanges applies the wizard's storage/backend/application
// choices to an existing config's root mapping node.
func applyWizardChanges(root *yaml.Node, req completeRequest) {
	setMappingKey(root, "storage", storageNode(req))
	if sdcppNode := getMappingValue(root, "sdcpp"); sdcppNode != nil {
		updateFirstBackend(sdcppNode, req)
	}
	if appNode := getMappingValue(root, "application"); appNode != nil {
		if req.Title != "" {
			setMappingKey(appNode, "title", strNode(req.Title))
		}
		if req.ProjectName != "" {
			setMappingKey(appNode, "default_project", strNode(req.ProjectName))
		}
	}
}

// applyProfileFlags sets a profile's extension enabled flags on an
// in-memory root node. Only the keys a profile lists are touched;
// unlisted keys keep their current state. The title is handled via
// req.Title; storage is never a profile's job.
func applyProfileFlags(root *yaml.Node, key string) error {
	p := GetProfile(key)
	if p == nil {
		return fmt.Errorf("unknown profile: %q", key)
	}
	extNode := getMappingValue(root, "extensions")
	if extNode == nil || extNode.Kind != yaml.MappingNode {
		extNode = &yaml.Node{Kind: yaml.MappingNode}
		setMappingKey(root, "extensions", extNode)
	}
	for k, on := range p.Enabled {
		child := getMappingValue(extNode, k)
		if child == nil || child.Kind != yaml.MappingNode {
			child = &yaml.Node{Kind: yaml.MappingNode}
			setMappingKey(extNode, k, child)
		}
		setMappingKey(child, "enabled", boolNode(on))
	}
	return nil
}

// writeFreshConfig writes a complete, readable config from the wizard's
// choices (first-run case: no file to preserve).
func (e *Extension) writeFreshConfig(path string, req completeRequest) error {
	return atomicWrite(path, marshalYAML(e.buildFreshConfig(req)))
}

// buildFreshConfig renders the first-run document. A selected profile
// contributes the extensions on/off section; the wizard's own fields
// (storage, backend, title, project) always apply.
func (e *Extension) buildFreshConfig(req completeRequest) *freshConfig {
	fc := &freshConfig{}
	fc.Server.Listen = e.a.Config.Server.Listen
	fc.Database.SQLitePath = e.a.Config.Database.SQLiteDatabase
	fc.SDCPP.Backends = []freshBackend{{
		Name:         "default",
		BaseURL:      req.BackendURL,
		Architecture: req.BackendArch,
	}}
	fc.Storage = storageFields(req)
	fc.Apply.Title = req.Title
	fc.Apply.DefaultProject = req.ProjectName
	fc.Extensions = profileExtensions(req.ProfileKey)
	return fc
}

// profileExtensions maps a profile key to its fresh-config extension
// section. Unknown or empty keys yield nil (no section).
func profileExtensions(key string) map[string]freshExt {
	p := GetProfile(key)
	if p == nil {
		return nil
	}
	m := make(map[string]freshExt, len(p.Enabled))
	for k, on := range p.Enabled {
		m[k] = freshExt{Enabled: on}
	}
	return m
}

// renderConfig renders the config file the wizard would produce for req
// without writing anything. When mask is true, non-empty secret values
// (S3 keys) are replaced with a mask — previews must never reveal
// secrets of the current config.
func (e *Extension) renderConfig(req completeRequest, mask bool) (string, error) {
	path := e.configPath()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		b := marshalYAML(e.buildFreshConfig(req))
		return string(b), nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read config: %w", err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return "", fmt.Errorf("parse existing config: %w", err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		// Empty or malformed document — render what a fresh write
		// would produce (same fallback as the write path).
		b := marshalYAML(e.buildFreshConfig(req))
		return string(b), nil
	}
	root := doc.Content[0]
	if req.ProfileKey != "" {
		if err := applyProfileFlags(root, req.ProfileKey); err != nil {
			return "", err
		}
	}
	applyWizardChanges(root, req)
	if mask {
		maskSecrets(&doc)
	}
	b := marshalYAML(&doc)
	return string(b), nil
}

// maskedValue replaces secret values in config previews.
const maskedValue = "••••••••"

// secretKeys are storage fields whose values must never appear in a
// config preview.
var secretKeys = []string{"access_key", "secret_key"}

// maskSecrets replaces non-empty S3 key values in a rendered document
// with maskedValue so previews never reveal secrets of the current
// config.
func maskSecrets(doc *yaml.Node) {
	if len(doc.Content) == 0 {
		return
	}
	st := getMappingValue(doc.Content[0], "storage")
	if st == nil {
		return
	}
	for _, k := range secretKeys {
		if v := getMappingValue(st, k); v != nil && v.Value != "" {
			v.Value = maskedValue
		}
	}
}

// storageFields builds the typed storage section for a fresh config.
func storageFields(req completeRequest) struct {
	Type           string `yaml:"type"`
	Capacity       string `yaml:"capacity,omitempty"`
	FilePath       string `yaml:"file_path,omitempty"`
	Endpoint       string `yaml:"endpoint,omitempty"`
	Region         string `yaml:"region,omitempty"`
	Bucket         string `yaml:"bucket,omitempty"`
	AccessKey      string `yaml:"access_key,omitempty"`
	SecretKey      string `yaml:"secret_key,omitempty"`
	ForcePathStyle bool   `yaml:"force_path_style,omitempty"`
} {
	var s struct {
		Type           string `yaml:"type"`
		Capacity       string `yaml:"capacity,omitempty"`
		FilePath       string `yaml:"file_path,omitempty"`
		Endpoint       string `yaml:"endpoint,omitempty"`
		Region         string `yaml:"region,omitempty"`
		Bucket         string `yaml:"bucket,omitempty"`
		AccessKey      string `yaml:"access_key,omitempty"`
		SecretKey      string `yaml:"secret_key,omitempty"`
		ForcePathStyle bool   `yaml:"force_path_style,omitempty"`
	}
	s.Type = req.StorageType
	switch req.StorageType {
	case "memory":
		s.Capacity = req.MemoryCapacity
	case "file":
		s.FilePath = req.FilePath
	case "s3":
		s.Endpoint = req.Endpoint
		s.Region = req.Region
		s.Bucket = req.Bucket
		s.AccessKey = req.AccessKey
		s.SecretKey = req.SecretKey
		s.ForcePathStyle = req.ForcePathStyle
	}
	return s
}

// storageNode builds the storage mapping node for in-place updates.
func storageNode(req completeRequest) *yaml.Node {
	m := &yaml.Node{Kind: yaml.MappingNode}
	add := func(k, v string) { m.Content = append(m.Content, strNode(k), strNode(v)) }
	add("type", req.StorageType)
	switch req.StorageType {
	case "memory":
		if req.MemoryCapacity != "" {
			add("capacity", req.MemoryCapacity)
		}
	case "file":
		add("file_path", req.FilePath)
	case "s3":
		add("endpoint", req.Endpoint)
		add("region", req.Region)
		add("bucket", req.Bucket)
		if req.AccessKey != "" {
			add("access_key", req.AccessKey)
		}
		if req.SecretKey != "" {
			add("secret_key", req.SecretKey)
		}
		if req.ForcePathStyle {
			m.Content = append(m.Content, strNode("force_path_style"), boolNode(true))
		}
	}
	return m
}

// updateFirstBackend updates the base_url (and architecture, when set)
// of the first sdcpp backend entry, leaving any additional backends
// untouched.
func updateFirstBackend(sdcpp *yaml.Node, req completeRequest) {
	backends := getMappingValue(sdcpp, "backends")
	if backends == nil || backends.Kind != yaml.SequenceNode || len(backends.Content) == 0 {
		// Legacy single base_url or missing list — create the list.
		entry := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
			strNode("name"), strNode("default"),
			strNode("base_url"), strNode(req.BackendURL),
		}}
		if req.BackendArch != "" {
			entry.Content = append(entry.Content, strNode("architecture"), strNode(req.BackendArch))
		}
		seq := &yaml.Node{Kind: yaml.SequenceNode, Content: []*yaml.Node{entry}}
		setMappingKey(sdcpp, "backends", seq)
		return
	}
	first := backends.Content[0]
	setMappingKey(first, "base_url", strNode(req.BackendURL))
	if req.BackendArch != "" {
		setMappingKey(first, "architecture", strNode(req.BackendArch))
	}
}

// freshBackend is one sdcpp backend entry in a fresh config.
type freshBackend struct {
	Name         string `yaml:"name"`
	BaseURL      string `yaml:"base_url"`
	Architecture string `yaml:"architecture,omitempty"`
}

// freshExt is one extension entry in a fresh config.
type freshExt struct {
	Enabled bool `yaml:"enabled"`
}

// freshConfig mirrors the config.yaml file layout for the first-run
// write.
type freshConfig struct {
	Server   struct {
		Listen string `yaml:"listen"`
	} `yaml:"server"`
	SDCPP struct {
		Backends []freshBackend `yaml:"backends"`
	} `yaml:"sdcpp"`
	Storage struct {
		Type           string `yaml:"type"`
		Capacity       string `yaml:"capacity,omitempty"`
		FilePath       string `yaml:"file_path,omitempty"`
		Endpoint       string `yaml:"endpoint,omitempty"`
		Region         string `yaml:"region,omitempty"`
		Bucket         string `yaml:"bucket,omitempty"`
		AccessKey      string `yaml:"access_key,omitempty"`
		SecretKey      string `yaml:"secret_key,omitempty"`
		ForcePathStyle bool   `yaml:"force_path_style,omitempty"`
	} `yaml:"storage"`
	Database struct {
		SQLitePath string `yaml:"sqlite_path"`
	} `yaml:"database"`
	Apply struct {
		Title          string `yaml:"title"`
		DefaultProject string `yaml:"default_project"`
	} `yaml:"application"`
	Extensions map[string]freshExt `yaml:"extensions,omitempty"`
}

// applyProfile updates the config file with a profile's title and
// extension enabled flags. Storage is never touched — storage setup is
// the wizard's job, not a profile's. When no file exists yet, one is
// created from the current effective (running) config first, so the
// result is a complete, bootable document.
func (e *Extension) applyProfile(p Profile) error {
	path := e.configPath()
	if _, err := os.Stat(path); err != nil {
		// No file yet: build one from the current effective config,
		// then let the in-place path apply the profile to it.
		if err := e.writeFreshConfig(path, completeRequest{
			StorageType:    e.a.Config.Storage.Type,
			MemoryCapacity: e.a.Config.Storage.Capacity,
			FilePath:       e.a.Config.Storage.FilePath,
			Endpoint:       e.a.Config.Storage.Endpoint,
			Region:         e.a.Config.Storage.Region,
			Bucket:         e.a.Config.Storage.Bucket,
			AccessKey:      e.a.Config.Storage.AccessKey,
			SecretKey:      e.a.Config.Storage.SecretKey,
			ForcePathStyle: e.a.Config.Storage.ForcePathStyle,
			BackendURL:     e.a.Config.SDCPP.Backends[0].BaseURL,
			BackendArch:    e.a.Config.BackendArchitecture(e.a.Config.DefaultBackend()),
			Title:          p.Title,
			ProjectName:    e.a.Config.Application.DefaultProject,
		}); err != nil {
			return err
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("parse existing config: %w", err)
	}
	root := doc.Content[0]

	if appNode := getMappingValue(root, "application"); appNode != nil {
		setMappingKey(appNode, "title", strNode(p.Title))
	} else {
		appNode := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{strNode("title"), strNode(p.Title)}}
		setMappingKey(root, "application", appNode)
	}

	// Extension enabled flags: only the keys a profile lists are
	// touched; unlisted keys keep their current state.
	extNode := getMappingValue(root, "extensions")
	if extNode == nil || extNode.Kind != yaml.MappingNode {
		extNode = &yaml.Node{Kind: yaml.MappingNode}
		setMappingKey(root, "extensions", extNode)
	}
	for key, on := range p.Enabled {
		child := getMappingValue(extNode, key)
		if child == nil || child.Kind != yaml.MappingNode {
			child = &yaml.Node{Kind: yaml.MappingNode}
			setMappingKey(extNode, key, child)
		}
		setMappingKey(child, "enabled", boolNode(on))
	}

	out := &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{root}}
	return atomicWrite(path, marshalYAML(out))
}

// --- YAML node helpers ---

func marshalYAML(v any) []byte {
	b, err := yaml.Marshal(v)
	if err != nil {
		// Marshal on a well-formed node tree does not fail in
		// practice; the caller gets an empty write rather than a panic.
		return nil
	}
	return b
}

func getMappingValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func setMappingKey(m *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = value
			return
		}
	}
	m.Content = append(m.Content, strNode(key), value)
}

func strNode(s string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s}
}

func boolNode(b bool) *yaml.Node {
	v := "false"
	if b {
		v = "true"
	}
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: v}
}

// atomicWrite writes data to path via temp file + rename with 0600
// permissions (the file may contain S3 secrets).
func atomicWrite(path string, data []byte) error {
	if len(data) == 0 {
		return fmt.Errorf("refusing to write empty config")
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp config: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after successful rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp config: %w", err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return fmt.Errorf("chmod config: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}
	return nil
}
