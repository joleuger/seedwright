package model

import (
	"encoding/json"
	"testing"
)

func TestProjectSettingsDelta_JSONObjectRoundTrip(t *testing.T) {
	// Regression: extFields is unexported, so default encoding/json would
	// drop it on marshal and the core's scoped save would wipe extension
	// fields from the authoritative S3 delta file.
	d := ProjectSettingsDelta{ID: "proj", Version: 3}
	d.SetField("print_enabled", true)
	d.SetField("max_photos", "5")
	d.SetField("post_filter_prompt", "a dog")

	data, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Marshal output must contain the extension fields.
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}
	if raw["id"] != "proj" || raw["version"] != float64(3) {
		t.Errorf("raw = %v, want id=proj version=3", raw)
	}
	if raw["print_enabled"] != true || raw["max_photos"] != "5" || raw["post_filter_prompt"] != "a dog" {
		t.Errorf("raw extension fields = %v", raw)
	}

	back, err := ProjectSettingsDeltaFromJSON(data)
	if err != nil {
		t.Fatalf("FromJSON: %v", err)
	}
	if back.ID != "proj" || back.Version != 3 {
		t.Errorf("back = %+v, want id=proj version=3", back)
	}
	if v := back.Field("print_enabled"); v != true {
		t.Errorf("print_enabled = %v, want true", v)
	}
	if v := back.Field("max_photos"); v != "5" {
		t.Errorf("max_photos = %v, want \"5\"", v)
	}
	if v := back.Field("post_filter_prompt"); v != "a dog" {
		t.Errorf("post_filter_prompt = %v, want \"a dog\"", v)
	}
}

func TestProjectSettingsDelta_JSONObjectNoFields(t *testing.T) {
	d := ProjectSettingsDelta{ID: "p", Version: 1}
	data, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var back ProjectSettingsDelta
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.ID != "p" || back.Version != 1 {
		t.Errorf("back = %+v, want id=p version=1", back)
	}
	if back.Field("anything") != nil {
		t.Errorf("expected no fields, got %v", back.Fields())
	}
}

func TestProjectSettingsDelta_UnknownKeysBecomeFields(t *testing.T) {
	var d ProjectSettingsDelta
	err := json.Unmarshal([]byte(`{"id":"x","version":2,"keep_on_cancel":false,"extra":1.5}`), &d)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if d.Field("keep_on_cancel") != false {
		t.Errorf("keep_on_cancel = %v, want false", d.Field("keep_on_cancel"))
	}
	if d.Field("extra") != 1.5 {
		t.Errorf("extra = %v, want 1.5", d.Field("extra"))
	}
	if d.Field("id") != nil || d.Field("version") != nil {
		t.Errorf("reserved keys must not become fields: %v", d.Fields())
	}
}
