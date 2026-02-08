package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hseinmoussa/claude-autopilot/internal/fileutil"
	"gopkg.in/yaml.v3"
)

// Config holds all runtime configuration for claude-autopilot.
type Config struct {
	SkipPermissions     bool          `yaml:"skip_permissions"`
	HangTimeout         time.Duration `yaml:"hang_timeout"`
	WebhookURL          string        `yaml:"webhook_url"`
	NotificationDesktop bool          `yaml:"notification_desktop"`
	NotificationBell    bool          `yaml:"notification_bell"`
}

// knownKeys lists every valid configuration key.
var knownKeys = map[string]bool{
	"skip_permissions":     true,
	"hang_timeout":         true,
	"webhook_url":          true,
	"notification_desktop": true,
	"notification_bell":    true,
}

// defaults returns a Config with all default values applied.
func defaults() Config {
	return Config{
		HangTimeout:      10 * time.Minute,
		NotificationBell: true,
	}
}

// configFileRaw is the on-disk representation. We keep it separate so we can
// handle duration strings ("10m") in YAML while storing a typed Config.
type configFileRaw struct {
	SkipPermissions     *bool   `yaml:"skip_permissions,omitempty"`
	HangTimeout         *string `yaml:"hang_timeout,omitempty"`
	WebhookURL          *string `yaml:"webhook_url,omitempty"`
	NotificationDesktop *bool   `yaml:"notification_desktop,omitempty"`
	NotificationBell    *bool   `yaml:"notification_bell,omitempty"`
}

// BaseDir returns the root configuration directory: ~/.claude-autopilot/
func BaseDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		// Fall back to HOME env var.
		home = os.Getenv("HOME")
	}
	return filepath.Join(home, ".claude-autopilot")
}

// EnsureDirs creates the full directory tree required by claude-autopilot:
// base, state, tasks, logs, control.
func EnsureDirs() error {
	base := BaseDir()
	dirs := []string{
		base,
		filepath.Join(base, "state"),
		filepath.Join(base, "tasks"),
		filepath.Join(base, "logs"),
		filepath.Join(base, "control"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			return fmt.Errorf("create dir %s: %w", d, err)
		}
	}
	return nil
}

// configFilePath returns the path to the main config file.
func configFilePath() string {
	return filepath.Join(BaseDir(), "config.yaml")
}

// Load reads configuration from disk and applies the resolution order:
//
//	CLI flag > env var > config file > default
//
// CLI flag overrides are passed via the overrides map (key -> string value).
func Load(overrides map[string]string) (Config, error) {
	cfg := defaults()

	// --- Layer 1: config file ---
	raw, err := loadRawFile()
	if err != nil {
		return cfg, err
	}
	applyFileToConfig(raw, &cfg)

	// --- Layer 2: environment variables ---
	applyEnvToConfig(&cfg)

	// --- Layer 3: CLI flag overrides ---
	if err := applyOverrides(overrides, &cfg); err != nil {
		return cfg, err
	}

	return cfg, nil
}

// loadRawFile reads and parses the YAML config file. If the file does not
// exist the returned struct is zero-valued (all pointers nil).
func loadRawFile() (configFileRaw, error) {
	var raw configFileRaw
	data, err := os.ReadFile(configFilePath())
	if err != nil {
		if os.IsNotExist(err) {
			return raw, nil
		}
		return raw, fmt.Errorf("read config file: %w", err)
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return raw, fmt.Errorf("parse config file: %w", err)
	}
	return raw, nil
}

func applyFileToConfig(raw configFileRaw, cfg *Config) {
	if raw.SkipPermissions != nil {
		cfg.SkipPermissions = *raw.SkipPermissions
	}
	if raw.HangTimeout != nil {
		if d, err := time.ParseDuration(*raw.HangTimeout); err == nil {
			cfg.HangTimeout = d
		}
	}
	if raw.WebhookURL != nil {
		cfg.WebhookURL = *raw.WebhookURL
	}
	if raw.NotificationDesktop != nil {
		cfg.NotificationDesktop = *raw.NotificationDesktop
	}
	if raw.NotificationBell != nil {
		cfg.NotificationBell = *raw.NotificationBell
	}
}

// applyEnvToConfig reads CLAUDE_AUTOPILOT_<UPPER_SNAKE_KEY> env vars.
func applyEnvToConfig(cfg *Config) {
	if v, ok := lookupEnv("skip_permissions"); ok {
		cfg.SkipPermissions = parseBool(v)
	}
	if v, ok := lookupEnv("hang_timeout"); ok {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.HangTimeout = d
		}
	}
	if v, ok := lookupEnv("webhook_url"); ok {
		cfg.WebhookURL = v
	}
	if v, ok := lookupEnv("notification_desktop"); ok {
		cfg.NotificationDesktop = parseBool(v)
	}
	if v, ok := lookupEnv("notification_bell"); ok {
		cfg.NotificationBell = parseBool(v)
	}
}

// lookupEnv checks for CLAUDE_AUTOPILOT_<UPPER_SNAKE_KEY>.
func lookupEnv(key string) (string, bool) {
	envKey := "CLAUDE_AUTOPILOT_" + strings.ToUpper(key)
	return os.LookupEnv(envKey)
}

func parseBool(s string) bool {
	switch strings.ToLower(s) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}

// applyOverrides applies CLI flag overrides.
func applyOverrides(overrides map[string]string, cfg *Config) error {
	for k, v := range overrides {
		if !knownKeys[k] {
			return fmt.Errorf("unknown config key: %s", k)
		}
		switch k {
		case "skip_permissions":
			cfg.SkipPermissions = parseBool(v)
		case "hang_timeout":
			d, err := time.ParseDuration(v)
			if err != nil {
				return fmt.Errorf("invalid hang_timeout %q: %w", v, err)
			}
			cfg.HangTimeout = d
		case "webhook_url":
			cfg.WebhookURL = v
		case "notification_desktop":
			cfg.NotificationDesktop = parseBool(v)
		case "notification_bell":
			cfg.NotificationBell = parseBool(v)
		}
	}
	return nil
}

// ValidateKey returns an error if key is not a known configuration key.
func ValidateKey(key string) error {
	if !knownKeys[key] {
		return fmt.Errorf("unknown config key %q; known keys: %s", key, knownKeysList())
	}
	return nil
}

func knownKeysList() string {
	keys := make([]string, 0, len(knownKeys))
	for k := range knownKeys {
		keys = append(keys, k)
	}
	return strings.Join(keys, ", ")
}

// SetConfigValue writes a key-value pair to the config file. The file is
// created if it does not exist. Uses atomic write for crash safety.
func SetConfigValue(key, value string) error {
	if err := ValidateKey(key); err != nil {
		return err
	}

	raw, err := loadRawFile()
	if err != nil {
		return err
	}

	setRawValue(&raw, key, value)

	data, err := yaml.Marshal(&raw)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	return fileutil.AtomicWrite(configFilePath(), data, 0644)
}

func setRawValue(raw *configFileRaw, key, value string) {
	switch key {
	case "skip_permissions":
		b := parseBool(value)
		raw.SkipPermissions = &b
	case "hang_timeout":
		raw.HangTimeout = &value
	case "webhook_url":
		raw.WebhookURL = &value
	case "notification_desktop":
		b := parseBool(value)
		raw.NotificationDesktop = &b
	case "notification_bell":
		b := parseBool(value)
		raw.NotificationBell = &b
	}
}

// GetConfigValue returns the current effective value of a config key as a
// string, after applying the full resolution order (file + env; no CLI flags).
func GetConfigValue(key string) (string, error) {
	if err := ValidateKey(key); err != nil {
		return "", err
	}

	cfg, err := Load(nil)
	if err != nil {
		return "", err
	}

	switch key {
	case "skip_permissions":
		return fmt.Sprintf("%t", cfg.SkipPermissions), nil
	case "hang_timeout":
		return cfg.HangTimeout.String(), nil
	case "webhook_url":
		return cfg.WebhookURL, nil
	case "notification_desktop":
		return fmt.Sprintf("%t", cfg.NotificationDesktop), nil
	case "notification_bell":
		return fmt.Sprintf("%t", cfg.NotificationBell), nil
	default:
		return "", fmt.Errorf("unknown key: %s", key)
	}
}

// ListConfig returns all config keys and their current effective values.
func ListConfig() (map[string]string, error) {
	cfg, err := Load(nil)
	if err != nil {
		return nil, err
	}

	return map[string]string{
		"skip_permissions":     fmt.Sprintf("%t", cfg.SkipPermissions),
		"hang_timeout":         cfg.HangTimeout.String(),
		"webhook_url":          cfg.WebhookURL,
		"notification_desktop": fmt.Sprintf("%t", cfg.NotificationDesktop),
		"notification_bell":    fmt.Sprintf("%t", cfg.NotificationBell),
	}, nil
}
