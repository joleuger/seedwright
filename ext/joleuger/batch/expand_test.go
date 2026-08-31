package batch

import (
	"testing"
)

func TestNeedsExpansion(t *testing.T) {
	e := &Extension{}

	tests := []struct {
		name     string
		prompt   string
		seeds    string
		needsExp bool
	}{
		{"no combo, no seeds", "a cat", "", false},
		{"no combo, single seed", "a cat", "42", false},
		{"no combo, -1 seed", "a cat", "-1", false},
		{"no combo, empty seeds", "a cat", "", false},
		{"combo, no seeds", "a {cat,dog}", "", true},
		{"combo, single seed", "a {cat,dog}", "42", true},
		{"combo, multiple seeds", "a {cat,dog}", "1,2,3", true},
		{"no combo, multiple seeds", "a cat", "1,2,3", true},
		{"no combo, range seeds", "a cat", "1..5", true},
		{"multiple combos", "A {car,bike} on {day,night}", "", true},
		{"combo + multiple seeds", "{a,b}", "1,2", true},
		{"combo + range seeds", "{a,b}", "1..3", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := e.NeedsExpansion(tt.prompt, tt.seeds)
			if got != tt.needsExp {
				t.Errorf("NeedsExpansion(%q, %q) = %v, want %v", tt.prompt, tt.seeds, got, tt.needsExp)
			}
		})
	}
}

func TestExpand(t *testing.T) {
	e := &Extension{}

	tests := []struct {
		name         string
		prompt       string
		seeds        string
		wantCount    int
		wantVariants []struct {
			prompt string
			seed   int64
		}
		wantErr bool
	}{
		{
			name:      "no combo, no seeds",
			prompt:    "a cat",
			seeds:     "",
			wantCount: 1,
			wantVariants: []struct {
				prompt string
				seed   int64
			}{{"a cat", -1}},
		},
		{
			name:      "no combo, single seed",
			prompt:    "a cat",
			seeds:     "42",
			wantCount: 1,
			wantVariants: []struct {
				prompt string
				seed   int64
			}{{"a cat", 42}},
		},
		{
			name:      "no combo, multiple seeds",
			prompt:    "a cat",
			seeds:     "1,2,3",
			wantCount: 3,
			wantVariants: []struct {
				prompt string
				seed   int64
			}{{"a cat", 1}, {"a cat", 2}, {"a cat", 3}},
		},
		{
			name:      "no combo, range seeds",
			prompt:    "a cat",
			seeds:     "1..3",
			wantCount: 3,
			wantVariants: []struct {
				prompt string
				seed   int64
			}{{"a cat", 1}, {"a cat", 2}, {"a cat", 3}},
		},
		{
			name:      "one group, no seeds",
			prompt:    "A {cat,dog}",
			seeds:     "",
			wantCount: 2,
			wantVariants: []struct {
				prompt string
				seed   int64
			}{{"A cat", -1}, {"A dog", -1}},
		},
		{
			name:      "one group, one seed",
			prompt:    "A {cat,dog}",
			seeds:     "42",
			wantCount: 2,
			wantVariants: []struct {
				prompt string
				seed   int64
			}{{"A cat", 42}, {"A dog", 42}},
		},
		{
			name:      "one group, multiple seeds",
			prompt:    "A {cat,dog}",
			seeds:     "1,2",
			wantCount: 4,
			wantVariants: []struct {
				prompt string
				seed   int64
			}{
				{"A cat", 1}, {"A dog", 1},
				{"A cat", 2}, {"A dog", 2},
			},
		},
		{
			name:      "two groups, no seeds",
			prompt:    "A {car,motorcycle} on a {road,path}",
			seeds:     "",
			wantCount: 4,
			wantVariants: []struct {
				prompt string
				seed   int64
			}{
				{"A car on a path", -1},
				{"A car on a road", -1},
				{"A motorcycle on a path", -1},
				{"A motorcycle on a road", -1},
			},
		},
		{
			name:      "two groups, one seed",
			prompt:    "A {car,motorcycle} on a {road,path}",
			seeds:     "99",
			wantCount: 4,
			wantVariants: []struct {
				prompt string
				seed   int64
			}{
				{"A car on a path", 99},
				{"A car on a road", 99},
				{"A motorcycle on a path", 99},
				{"A motorcycle on a road", 99},
			},
		},
		{
			name:      "three groups, two seeds",
			prompt:    "A {red,blue} {cat,dog} on {day,night}",
			seeds:     "1,2",
			wantCount: 16, // 2*2*2*2
			wantVariants: []struct {
				prompt string
				seed   int64
			}{
				{"A blue cat on day", 1},
				{"A blue cat on night", 1},
				{"A blue dog on day", 1},
				{"A blue dog on night", 1},
				{"A red cat on day", 1},
				{"A red cat on night", 1},
				{"A red dog on day", 1},
				{"A red dog on night", 1},
				{"A blue cat on day", 2},
				{"A blue cat on night", 2},
				{"A blue dog on day", 2},
				{"A blue dog on night", 2},
				{"A red cat on day", 2},
				{"A red cat on night", 2},
				{"A red dog on day", 2},
				{"A red dog on night", 2},
			},
		},
		{
			name:      "empty group returns error",
			prompt:    "A {}",
			seeds:     "",
			wantCount: 0,
			wantErr:   true,
		},
		{
			name:      "combo with whitespace trimmed",
			prompt:    "A { cat , dog }",
			seeds:     "",
			wantCount: 2,
			wantVariants: []struct {
				prompt string
				seed   int64
			}{{"A cat", -1}, {"A dog", -1}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			variants, err := e.Expand(tt.prompt, tt.seeds)
			if tt.wantErr {
				if err == nil {
					t.Errorf("Expand(%q, %q) expected error, got nil", tt.prompt, tt.seeds)
				}
				return
			}
			if err != nil {
				t.Fatalf("Expand(%q, %q) unexpected error: %v", tt.prompt, tt.seeds, err)
			}
			if len(variants) != tt.wantCount {
				t.Errorf("Expand(%q, %q) = %d variants, want %d", tt.prompt, tt.seeds, len(variants), tt.wantCount)
				for i, v := range variants {
					t.Logf("  [%d] prompt=%q seed=%d", i, v.Prompt, v.Seed)
				}
				return
			}
			for i, want := range tt.wantVariants {
				if variants[i].Prompt != want.prompt {
					t.Errorf("variants[%d].Prompt = %q, want %q", i, variants[i].Prompt, want.prompt)
				}
				if variants[i].Seed != want.seed {
					t.Errorf("variants[%d].Seed = %d, want %d", i, variants[i].Seed, want.seed)
				}
			}
		})
	}
}

func TestParseSeeds(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    []int64
		wantNil bool
	}{
		{"empty", "", nil, true},
		{"single", "42", []int64{42}, false},
		{"comma list", "1,2,3", []int64{1, 2, 3}, false},
		{"comma with spaces", " 1 , 2 , 3 ", []int64{1, 2, 3}, false},
		{"range", "1..5", []int64{1, 2, 3, 4, 5}, false},
		{"reverse range", "5..1", nil, true},
		{"invalid comma", "1,abc,3", []int64{1, 3}, false},
		{"negative", "-1", []int64{-1}, false},
		{"mixed valid invalid", "1,abc,3", []int64{1, 3}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseSeeds(tt.input)
			if tt.wantNil {
				if got != nil && len(got) > 0 {
					t.Errorf("parseSeeds(%q) = %v, want nil/empty", tt.input, got)
				}
				return
			}
			if len(got) != len(tt.want) {
				t.Errorf("parseSeeds(%q) = %d items, want %d", tt.input, len(got), len(tt.want))
				return
			}
			for i, w := range tt.want {
				if got[i] != w {
					t.Errorf("parseSeeds(%q)[%d] = %d, want %d", tt.input, i, got[i], w)
				}
			}
		})
	}
}

func TestExpand_MultiWordCombo(t *testing.T) {
	e := &Extension{}

	tests := []struct {
		name     string
		prompt   string
		seeds    string
		wantCount int
		wantVariants []struct {
			prompt string
			seed   int64
		}
	}{
		{
			name:      "multi-word combo with trailing text",
			prompt:    "{car, motorcycle} is a vehicle",
			seeds:     "",
			wantCount: 2,
			wantVariants: []struct {
				prompt string
				seed   int64
			}{{"car is a vehicle", -1}, {"motorcycle is a vehicle", -1}},
		},
		{
			name:      "multi-word combo with surrounding text",
			prompt:    "A {red, blue} {car, motorcycle}",
			seeds:     "",
			wantCount: 4,
			wantVariants: []struct {
				prompt string
				seed   int64
			}{
				{"A blue car", -1},
				{"A blue motorcycle", -1},
				{"A red car", -1},
				{"A red motorcycle", -1},
			},
		},
		{
			name:      "multi-word combo with seeds",
			prompt:    "{car, motorcycle}",
			seeds:     "1,2",
			wantCount: 4,
			wantVariants: []struct {
				prompt string
				seed   int64
			}{
				{"car", 1},
				{"motorcycle", 1},
				{"car", 2},
				{"motorcycle", 2},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			variants, err := e.Expand(tt.prompt, tt.seeds)
			if err != nil {
				t.Fatalf("Expand(%q, %q) unexpected error: %v", tt.prompt, tt.seeds, err)
			}
			if len(variants) != tt.wantCount {
				t.Errorf("Expand(%q, %q) = %d variants, want %d", tt.prompt, tt.seeds, len(variants), tt.wantCount)
				for i, v := range variants {
					t.Logf("  [%d] prompt=%q seed=%d", i, v.Prompt, v.Seed)
				}
				return
			}
			for i, want := range tt.wantVariants {
				if variants[i].Prompt != want.prompt {
					t.Errorf("variants[%d].Prompt = %q, want %q", i, variants[i].Prompt, want.prompt)
				}
				if variants[i].Seed != want.seed {
					t.Errorf("variants[%d].Seed = %d, want %d", i, variants[i].Seed, want.seed)
				}
			}
		})
	}
}

func TestExtractGroups(t *testing.T) {
	tests := []struct {
		name    string
		prompt  string
		want    [][]string
		wantErr bool
	}{
		{"no groups", "a cat", [][]string{{"a cat"}}, false},
		{"one group", "A {cat,dog}", [][]string{{"A "}, {"cat", "dog"}}, false},
		{"two groups", "{red,blue} {cat,dog}", [][]string{{"red", "blue"}, {" "}, {"cat", "dog"}}, false},
		{"empty group", "A {}", nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := extractGroups(tt.prompt)
			if tt.wantErr {
				if err == nil {
					t.Error("extractGroups expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("extractGroups unexpected error: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Errorf("extractGroups = %d groups, want %d", len(got), len(tt.want))
				return
			}
			for i, w := range tt.want {
				if len(got[i]) != len(w) {
					t.Errorf("group[%d] = %d options, want %d", i, len(got[i]), len(w))
					continue
				}
				for j, wo := range w {
					if got[i][j] != wo {
						t.Errorf("group[%d][%d] = %q, want %q", i, j, got[i][j], wo)
					}
				}
			}
		})
	}
}

func TestCartesianProduct(t *testing.T) {
	tests := []struct {
		name  string
		groups [][]string
		want   []string
	}{
		{"empty groups", [][]string{}, []string{""}},
		{"one group", [][]string{{"A", "B"}}, []string{"A", "B"}},
		{"two groups", [][]string{{"A", "B"}, {"X", "Y"}}, []string{"AX", "AY", "BX", "BY"}},
		{"three groups", [][]string{{"A", "B"}, {"X", "Y"}, {"1", "2"}},
			[]string{"AX1", "AX2", "AY1", "AY2", "BX1", "BX2", "BY1", "BY2"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cartesianProduct(tt.groups)
			if len(got) != len(tt.want) {
				t.Errorf("cartesianProduct = %d results, want %d", len(got), len(tt.want))
				return
			}
			for i, w := range tt.want {
				if got[i] != w {
					t.Errorf("result[%d] = %q, want %q", i, got[i], w)
				}
			}
		})
	}
}
