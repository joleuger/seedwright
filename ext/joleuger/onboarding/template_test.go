package onboarding

import (
	"bytes"
	"strings"
	"testing"
)

// TestOnboardingTemplate_Renders parses and executes the page template
// with a populated State — template syntax errors only surface at
// render time, not at parse time.
func TestOnboardingTemplate_Renders(t *testing.T) {
	tmpl, err := onboardingTemplate()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	st := State{
		ConfigFileExists: false,
		ConfigPath:       "config.yaml",
		StorageType:      "memory",
		StorageOK:        true,
		StorageDetail:    "ephemeral memory — 0 B of 10.0MiB used, lost on restart",
		BackendName:      "default",
		BackendURL:       "http://127.0.0.1:1234",
		BackendOK:        false,
		BackendDetail:    "connection refused — is sdcpp running?",
		Title:            "seedwright",
		ProjectName:      "default",
		Profiles:         Profiles,
	}
	var buf bytes.Buffer
	err = tmpl.Execute(&buf, map[string]any{
		"Title":  "Setup & Customize",
		"prefix": "",
		"State":  st,
		"OnboardingJS": scriptJSON(scriptPayload{
			ConfigExists:    st.ConfigFileExists,
			WriteAllowed:    true,
			ConfirmRequired: false,
		}),
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := buf.String()

	mustContain := []string{
		"<title>Setup &amp; Customize</title>",
		"Current setup",
		"Config write",
		// profiles-first: the first question is "what do you want"
		"What do you actually want?",
		"Use this profile",
		// the link box pointing at the customization doc
		"customize-box",
		"https://github.com/joleuger/seedwright/blob/main/CUSTOMIZE.md",
		"Where do your images live?",
		"Where does sdcpp run?",
		"3 · Name it",
		// config preview card: write + download + confirm checkbox
		"Config preview",
		"cfgPreview",
		"Write config file",
		"Download config file",
		"confirmOverwrite",
		// placeholder docs links (anchors to be filled in by the maintainer)
		"https://github.com/joleuger/vuinputd/blob/main/docs/USAGE.md#installing-on-linux",
		"#installing-on-windows",
		"#model-selection",
		// every profile rendered
		"Photobooth", "Family Photos", "Image Box",
		// the JS payload is a single object literal
		"const ONBOARDING = {",
	}
	for _, want := range mustContain {
		if !strings.Contains(out, want) {
			t.Errorf("rendered page missing %q", want)
		}
	}
	// The scenario catalog no longer lives in the page — it moved to
	// CUSTOMIZE.md. Nothing scenario-related may render.
	for _, gone := range []string{
		"Not what you want?",
		"Describe your own",
		"Story book for a 6-year-old",
		"Telegram image chatbot",
	} {
		if strings.Contains(out, gone) {
			t.Errorf("rendered page still contains %q (moved to CUSTOMIZE.md)", gone)
		}
	}
}
