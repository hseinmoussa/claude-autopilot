package config

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

//go:embed matchers.default.yaml
var defaultMatchersYAML []byte

// MatchersConfig holds pattern lists used by the detector to identify rate
// limits and interactive prompts in Claude CLI output.
type MatchersConfig struct {
	RateLimitPatterns []string `yaml:"rate_limit_patterns"`
	PromptPatterns    []string `yaml:"prompt_patterns"`

	// Exclude lists let user overrides selectively remove default patterns.
	ExcludeRateLimitPatterns []string `yaml:"exclude_rate_limit_patterns,omitempty"`
	ExcludePromptPatterns    []string `yaml:"exclude_prompt_patterns,omitempty"`
}

// LoadMatchers loads the merged matcher configuration. Defaults are read from
// the embedded matchers.default.yaml. User overrides from
// ~/.claude-autopilot/matchers.yaml extend the default lists; exclude lists
// remove entries from the defaults.
func LoadMatchers() (MatchersConfig, error) {
	// Parse embedded defaults.
	var base MatchersConfig
	if err := yaml.Unmarshal(defaultMatchersYAML, &base); err != nil {
		return base, fmt.Errorf("parse default matchers: %w", err)
	}

	// Parse user overrides (optional).
	userPath := filepath.Join(BaseDir(), "matchers.yaml")
	data, err := os.ReadFile(userPath)
	if err != nil {
		if os.IsNotExist(err) {
			return base, nil
		}
		return base, fmt.Errorf("read user matchers file: %w", err)
	}

	var user MatchersConfig
	if err := yaml.Unmarshal(data, &user); err != nil {
		return base, fmt.Errorf("parse user matchers: %w", err)
	}

	return merge(base, user), nil
}

// merge combines base patterns with user overrides. User patterns extend
// the default lists. Exclude lists remove matching entries from the defaults.
func merge(base, user MatchersConfig) MatchersConfig {
	// Build exclude sets.
	excludeRL := toSet(user.ExcludeRateLimitPatterns)
	excludePR := toSet(user.ExcludePromptPatterns)

	// Filter defaults through exclude sets.
	result := MatchersConfig{
		RateLimitPatterns: filterExcluded(base.RateLimitPatterns, excludeRL),
		PromptPatterns:    filterExcluded(base.PromptPatterns, excludePR),
	}

	// Append user additions (deduplicated against existing entries).
	result.RateLimitPatterns = appendUnique(result.RateLimitPatterns, user.RateLimitPatterns)
	result.PromptPatterns = appendUnique(result.PromptPatterns, user.PromptPatterns)

	return result
}

func toSet(items []string) map[string]bool {
	s := make(map[string]bool, len(items))
	for _, item := range items {
		s[item] = true
	}
	return s
}

func filterExcluded(patterns []string, exclude map[string]bool) []string {
	out := make([]string, 0, len(patterns))
	for _, p := range patterns {
		if !exclude[p] {
			out = append(out, p)
		}
	}
	return out
}

func appendUnique(base, additions []string) []string {
	existing := toSet(base)
	for _, a := range additions {
		if !existing[a] {
			base = append(base, a)
			existing[a] = true
		}
	}
	return base
}
