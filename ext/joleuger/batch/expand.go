package batch

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// comboRegex matches a single combination group: {a,b,c}.
var comboRegex = regexp.MustCompile(`\{([^}]*)\}`)

// NeedsExpansion reports whether the prompt or seeds require
// expansion into multiple variants.
func (e *Extension) NeedsExpansion(prompt, seeds string) bool {
	return comboRegex.MatchString(prompt) || len(parseSeeds(seeds)) > 1
}

// Expand returns all (prompt, seed) pairs for the cartesian product
// of prompt groups × seeds, in the order a user would expect to see
// in a preview.
func (e *Extension) Expand(prompt, seeds string) ([]Variant, error) {
	groups, err := extractGroups(prompt)
	if err != nil {
		return nil, err
	}

	seedsList := parseSeeds(seeds)

	// If no groups and no/one seed, return single variant.
	if len(groups) == 0 {
		if len(seedsList) == 0 {
			return []Variant{{Prompt: prompt, Seed: -1}}, nil
		}
		variants := make([]Variant, len(seedsList))
		for i, s := range seedsList {
			variants[i] = Variant{Prompt: prompt, Seed: s}
		}
		return variants, nil
	}

	// If no explicit seeds, default to -1.
	if len(seedsList) == 0 {
		seedsList = []int64{-1}
	}

	// Cartesian product of groups × seeds.
	// Outer loop over seeds, inner over prompt variants — so the preview
	// groups all seeds for prompt variant N before showing prompt variant N+1.
	promptVariants := cartesianProduct(groups)
	variants := make([]Variant, 0, len(promptVariants)*len(seedsList))
	for _, s := range seedsList {
		for _, p := range promptVariants {
			variants = append(variants, Variant{Prompt: p, Seed: s})
		}
	}

	return variants, nil
}

// extractGroups finds all {a,b,c} groups in the prompt and returns
// the list of option slices (one per group), preserving order.
func extractGroups(prompt string) ([][]string, error) {
	groups := [][]string{}
	lastEnd := 0

	for _, submatch := range comboRegex.FindAllStringSubmatchIndex(prompt, -1) {
		// Append literal text before this group.
		if submatch[0] > lastEnd {
			groups = append(groups, []string{prompt[lastEnd:submatch[0]]})
		}

		// Parse options inside the braces.
		inner := prompt[submatch[2]:submatch[3]]
		options := strings.Split(inner, ",")
		for i, opt := range options {
			options[i] = strings.TrimSpace(opt)
		}
		if len(options) == 0 || (len(options) == 1 && options[0] == "") {
			return nil, fmt.Errorf("empty combination group at position %d", submatch[0])
		}
		groups = append(groups, options)

		lastEnd = submatch[1]
	}

	// Append remaining text after the last group.
	if lastEnd < len(prompt) {
		groups = append(groups, []string{prompt[lastEnd:]})
	}

	return groups, nil
}

// cartesianProduct computes the cartesian product of string slices,
// returning all concatenated results.
//
// Example: groups = [["A ", "B "], ["X", "Y"]] → ["A X", "A Y", "B X", "B Y"]
func cartesianProduct(groups [][]string) []string {
	if len(groups) == 0 {
		return []string{""}
	}

	// Start with the first group.
	results := make([]string, len(groups[0]))
	copy(results, groups[0])

	for i := 1; i < len(groups); i++ {
		next := make([]string, 0, len(results)*len(groups[i]))
		for _, r := range results {
			for _, g := range groups[i] {
				next = append(next, r+g)
			}
		}
		results = next
	}

	// Sort for deterministic preview order.
	sort.Strings(results)
	return results
}

// parseSeeds parses seed strings into int64 values.
// Supports: single number, comma-separated list, or range (e.g. "1..10").
func parseSeeds(s string) []int64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}

	idx := strings.Index(s, "..")
	if idx > 0 {
		start, err1 := strconv.ParseInt(s[:idx], 10, 64)
		end, err2 := strconv.ParseInt(s[idx+2:], 10, 64)
		if err1 != nil || err2 != nil {
			// Fall through to comma-parse.
			goto comma
		}
		if end < start {
			return nil
		}
		seeds := make([]int64, 0, int(end-start)+1)
		for i := start; i <= end; i++ {
			seeds = append(seeds, i)
		}
		return seeds
	}

comma:
	parts := strings.Split(s, ",")
	seeds := make([]int64, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		val, err := strconv.ParseInt(part, 10, 64)
		if err != nil {
			continue // skip invalid
		}
		seeds = append(seeds, val)
	}
	return seeds
}
