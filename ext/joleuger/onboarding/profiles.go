package onboarding

// Profile is a named preset of extension enabled flags plus an
// application title. Profiles are toggles: they write
// extensions.<key>.enabled for the keys they list and never touch
// storage or backends — storage is the wizard's job.
type Profile struct {
	Key    string
	Title  string
	Blurb  string
	// Enabled lists the extension keys a profile decides on. Keys not
	// listed are left at their current setting.
	Enabled map[string]bool
}

// Profiles is the shipped catalog. The order is the display order.
var Profiles = []Profile{
	{
		Key:   "try-it",
		Title: "Seedwright",
		Blurb: "Everything on. Explore the full feature set, turn things off later.",
		Enabled: map[string]bool{
			"joleuger/batch":                  true,
			"joleuger/console_code_authorizer": true,
			"joleuger/favorites":              true,
			"joleuger/imageproc":              true,
			"joleuger/photobooth":             true,
			"joleuger/printer":                true,
			"joleuger/slideshow":              true,
			"joleuger/authz-simple":           true,
			"joleuger/onboarding":             true,
		},
	},
	{
		Key:   "photobooth",
		Title: "Photobooth",
		Blurb: "Capture, print, and keep. The camera app for a physical printer.",
		Enabled: map[string]bool{
			"joleuger/photobooth":             true,
			"joleuger/printer":                true,
			"joleuger/imageproc":              true,
			"joleuger/favorites":              true,
			"joleuger/batch":                  true,
			"joleuger/onboarding":             true,
			"joleuger/console_code_authorizer": false,
			"joleuger/slideshow":              false,
			"joleuger/authz-simple":           false,
		},
	},
	{
		Key:   "family-archive",
		Title: "Family Photos",
		Blurb: "A safe, private place for the family pictures. Upload, browse, favorite.",
		Enabled: map[string]bool{
			"joleuger/favorites":  true,
			"joleuger/onboarding": true,
			"joleuger/photobooth": false,
			"joleuger/printer":    false,
			"joleuger/imageproc":  false,
			"joleuger/batch":      false,
			"joleuger/slideshow":  false,
		},
	},
	{
		Key:   "minimal",
		Title: "Image Box",
		Blurb: "The bare minimum: generate images, browse the gallery. Everything else off.",
		Enabled: map[string]bool{
			"joleuger/onboarding":             true,
			"joleuger/batch":                  false,
			"joleuger/console_code_authorizer": false,
			"joleuger/favorites":              false,
			"joleuger/imageproc":              false,
			"joleuger/photobooth":             false,
			"joleuger/printer":                false,
			"joleuger/slideshow":              false,
			"joleuger/authz-simple":           false,
		},
	},
}

// GetProfile returns the profile with the given key, or nil.
func GetProfile(key string) *Profile {
	for i := range Profiles {
		if Profiles[i].Key == key {
			return &Profiles[i]
		}
	}
	return nil
}
