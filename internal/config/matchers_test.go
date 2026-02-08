package config

import (
	"os"
	"path/filepath"
	"testing"
)

// ---------------------------------------------------------------------------
// LoadMatchers defaults
// ---------------------------------------------------------------------------

func TestLoadMatchers_DefaultPatterns(t *testing.T) {
	// Point HOME to a temp dir with no user matchers file.
	dir := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", dir)
	defer os.Setenv("HOME", origHome)

	mc, err := LoadMatchers()
	if err != nil {
		t.Fatalf("LoadMatchers: %v", err)
	}

	if len(mc.RateLimitPatterns) == 0 {
		t.Error("expected default rate limit patterns, got none")
	}
	if len(mc.PromptPatterns) == 0 {
		t.Error("expected default prompt patterns, got none")
	}

	// Check a known default pattern is present.
	found := false
	for _, p := range mc.RateLimitPatterns {
		if p == "rate limit" {
			found = true
			break
		}
	}
	if !found {
		t.Error("default RateLimitPatterns should contain \"rate limit\"")
	}
}

// ---------------------------------------------------------------------------
// Merge with user overrides
// ---------------------------------------------------------------------------

func TestLoadMatchers_UserOverridesExtend(t *testing.T) {
	dir := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", dir)
	defer os.Setenv("HOME", origHome)

	confDir := filepath.Join(dir, ".claude-autopilot")
	os.MkdirAll(confDir, 0755)

	userYAML := `
rate_limit_patterns:
  - "custom rate limit pattern"
prompt_patterns:
  - "custom prompt pattern"
`
	os.WriteFile(filepath.Join(confDir, "matchers.yaml"), []byte(userYAML), 0644)

	mc, err := LoadMatchers()
	if err != nil {
		t.Fatalf("LoadMatchers: %v", err)
	}

	// Should contain both defaults and user additions.
	foundCustomRL := false
	for _, p := range mc.RateLimitPatterns {
		if p == "custom rate limit pattern" {
			foundCustomRL = true
			break
		}
	}
	if !foundCustomRL {
		t.Error("user rate limit pattern not found in merged result")
	}

	foundCustomPR := false
	for _, p := range mc.PromptPatterns {
		if p == "custom prompt pattern" {
			foundCustomPR = true
			break
		}
	}
	if !foundCustomPR {
		t.Error("user prompt pattern not found in merged result")
	}

	// Default patterns should still be present.
	foundDefault := false
	for _, p := range mc.RateLimitPatterns {
		if p == "rate limit" {
			foundDefault = true
			break
		}
	}
	if !foundDefault {
		t.Error("default rate limit pattern should still be present after merge")
	}
}

// ---------------------------------------------------------------------------
// Exclude lists
// ---------------------------------------------------------------------------

func TestLoadMatchers_ExcludeRemovesPatterns(t *testing.T) {
	dir := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", dir)
	defer os.Setenv("HOME", origHome)

	confDir := filepath.Join(dir, ".claude-autopilot")
	os.MkdirAll(confDir, 0755)

	userYAML := `
exclude_rate_limit_patterns:
  - "429"
exclude_prompt_patterns:
  - "(Y/n)"
`
	os.WriteFile(filepath.Join(confDir, "matchers.yaml"), []byte(userYAML), 0644)

	mc, err := LoadMatchers()
	if err != nil {
		t.Fatalf("LoadMatchers: %v", err)
	}

	for _, p := range mc.RateLimitPatterns {
		if p == "429" {
			t.Error("excluded pattern \"429\" should not be present")
		}
	}

	for _, p := range mc.PromptPatterns {
		if p == "(Y/n)" {
			t.Error("excluded pattern \"(Y/n)\" should not be present")
		}
	}
}

// ---------------------------------------------------------------------------
// merge function directly
// ---------------------------------------------------------------------------

func TestMerge_DeduplicatesUserAdditions(t *testing.T) {
	base := MatchersConfig{
		RateLimitPatterns: []string{"a", "b"},
		PromptPatterns:    []string{"x"},
	}
	user := MatchersConfig{
		RateLimitPatterns: []string{"b", "c"},
		PromptPatterns:    []string{"x", "y"},
	}

	result := merge(base, user)

	// "b" should appear exactly once.
	count := 0
	for _, p := range result.RateLimitPatterns {
		if p == "b" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("\"b\" appears %d times in RateLimitPatterns; want 1", count)
	}

	// "x" should appear exactly once.
	count = 0
	for _, p := range result.PromptPatterns {
		if p == "x" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("\"x\" appears %d times in PromptPatterns; want 1", count)
	}

	// "c" and "y" should be added.
	foundC := false
	for _, p := range result.RateLimitPatterns {
		if p == "c" {
			foundC = true
		}
	}
	if !foundC {
		t.Error("\"c\" should be added to RateLimitPatterns")
	}

	foundY := false
	for _, p := range result.PromptPatterns {
		if p == "y" {
			foundY = true
		}
	}
	if !foundY {
		t.Error("\"y\" should be added to PromptPatterns")
	}
}
