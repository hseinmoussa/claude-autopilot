package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Load with defaults
// ---------------------------------------------------------------------------

func TestLoad_Defaults(t *testing.T) {
	// Point BaseDir to a temp directory with no config file.
	dir := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", dir)
	defer os.Setenv("HOME", origHome)

	// Clear any env vars that could affect the test.
	for _, key := range []string{
		"CLAUDE_AUTOPILOT_SKIP_PERMISSIONS",
		"CLAUDE_AUTOPILOT_HANG_TIMEOUT",
		"CLAUDE_AUTOPILOT_WEBHOOK_URL",
		"CLAUDE_AUTOPILOT_NOTIFICATION_DESKTOP",
		"CLAUDE_AUTOPILOT_NOTIFICATION_BELL",
	} {
		os.Unsetenv(key)
	}

	cfg, err := Load(nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.SkipPermissions != false {
		t.Errorf("SkipPermissions default = %v; want false", cfg.SkipPermissions)
	}
	if cfg.HangTimeout != 10*time.Minute {
		t.Errorf("HangTimeout default = %v; want 10m", cfg.HangTimeout)
	}
	if cfg.WebhookURL != "" {
		t.Errorf("WebhookURL default = %q; want empty", cfg.WebhookURL)
	}
	if cfg.NotificationDesktop != false {
		t.Errorf("NotificationDesktop default = %v; want false", cfg.NotificationDesktop)
	}
	if cfg.NotificationBell != true {
		t.Errorf("NotificationBell default = %v; want true", cfg.NotificationBell)
	}
}

func TestLoad_FromFile(t *testing.T) {
	dir := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", dir)
	defer os.Setenv("HOME", origHome)

	// Clear env vars.
	for _, key := range []string{
		"CLAUDE_AUTOPILOT_SKIP_PERMISSIONS",
		"CLAUDE_AUTOPILOT_HANG_TIMEOUT",
		"CLAUDE_AUTOPILOT_WEBHOOK_URL",
		"CLAUDE_AUTOPILOT_NOTIFICATION_DESKTOP",
		"CLAUDE_AUTOPILOT_NOTIFICATION_BELL",
	} {
		os.Unsetenv(key)
	}

	confDir := filepath.Join(dir, ".claude-autopilot")
	os.MkdirAll(confDir, 0755)
	confFile := filepath.Join(confDir, "config.yaml")
	content := `skip_permissions: true
hang_timeout: "5m"
webhook_url: "https://example.com/hook"
notification_desktop: true
notification_bell: false
`
	os.WriteFile(confFile, []byte(content), 0644)

	cfg, err := Load(nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if !cfg.SkipPermissions {
		t.Error("SkipPermissions should be true from file")
	}
	if cfg.HangTimeout != 5*time.Minute {
		t.Errorf("HangTimeout = %v; want 5m", cfg.HangTimeout)
	}
	if cfg.WebhookURL != "https://example.com/hook" {
		t.Errorf("WebhookURL = %q; want https://example.com/hook", cfg.WebhookURL)
	}
	if !cfg.NotificationDesktop {
		t.Error("NotificationDesktop should be true from file")
	}
	if cfg.NotificationBell {
		t.Error("NotificationBell should be false from file")
	}
}

func TestLoad_OverridesApplied(t *testing.T) {
	dir := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", dir)
	defer os.Setenv("HOME", origHome)

	// Clear env vars.
	for _, key := range []string{
		"CLAUDE_AUTOPILOT_SKIP_PERMISSIONS",
		"CLAUDE_AUTOPILOT_HANG_TIMEOUT",
		"CLAUDE_AUTOPILOT_WEBHOOK_URL",
		"CLAUDE_AUTOPILOT_NOTIFICATION_DESKTOP",
		"CLAUDE_AUTOPILOT_NOTIFICATION_BELL",
	} {
		os.Unsetenv(key)
	}

	overrides := map[string]string{
		"skip_permissions": "true",
		"hang_timeout":     "30s",
	}

	cfg, err := Load(overrides)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if !cfg.SkipPermissions {
		t.Error("SkipPermissions should be true from override")
	}
	if cfg.HangTimeout != 30*time.Second {
		t.Errorf("HangTimeout = %v; want 30s", cfg.HangTimeout)
	}
}

func TestLoad_UnknownOverrideKey(t *testing.T) {
	dir := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", dir)
	defer os.Setenv("HOME", origHome)

	overrides := map[string]string{
		"nonexistent_key": "value",
	}

	_, err := Load(overrides)
	if err == nil {
		t.Fatal("expected error for unknown override key")
	}
}

// ---------------------------------------------------------------------------
// ValidateKey
// ---------------------------------------------------------------------------

func TestValidateKey_Known(t *testing.T) {
	keys := []string{
		"skip_permissions",
		"hang_timeout",
		"webhook_url",
		"notification_desktop",
		"notification_bell",
	}
	for _, k := range keys {
		if err := ValidateKey(k); err != nil {
			t.Errorf("ValidateKey(%q) = %v; want nil", k, err)
		}
	}
}

func TestValidateKey_Unknown(t *testing.T) {
	unknowns := []string{"bad_key", "", "SkipPermissions", "HANG_TIMEOUT"}
	for _, k := range unknowns {
		if err := ValidateKey(k); err == nil {
			t.Errorf("ValidateKey(%q) = nil; want error", k)
		}
	}
}

// ---------------------------------------------------------------------------
// SetConfigValue / GetConfigValue roundtrip
// ---------------------------------------------------------------------------

func TestSetGetConfigValue_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", dir)
	defer os.Setenv("HOME", origHome)

	// Clear env vars.
	for _, key := range []string{
		"CLAUDE_AUTOPILOT_SKIP_PERMISSIONS",
		"CLAUDE_AUTOPILOT_HANG_TIMEOUT",
		"CLAUDE_AUTOPILOT_WEBHOOK_URL",
		"CLAUDE_AUTOPILOT_NOTIFICATION_DESKTOP",
		"CLAUDE_AUTOPILOT_NOTIFICATION_BELL",
	} {
		os.Unsetenv(key)
	}

	// Create the config directory.
	os.MkdirAll(filepath.Join(dir, ".claude-autopilot"), 0755)

	if err := SetConfigValue("webhook_url", "https://hooks.example.com"); err != nil {
		t.Fatalf("SetConfigValue: %v", err)
	}

	val, err := GetConfigValue("webhook_url")
	if err != nil {
		t.Fatalf("GetConfigValue: %v", err)
	}
	if val != "https://hooks.example.com" {
		t.Errorf("GetConfigValue = %q; want https://hooks.example.com", val)
	}
}

func TestSetConfigValue_InvalidKey(t *testing.T) {
	err := SetConfigValue("not_a_key", "value")
	if err == nil {
		t.Fatal("expected error for invalid key")
	}
}

func TestGetConfigValue_InvalidKey(t *testing.T) {
	_, err := GetConfigValue("not_a_key")
	if err == nil {
		t.Fatal("expected error for invalid key")
	}
}

// ---------------------------------------------------------------------------
// ListConfig
// ---------------------------------------------------------------------------

func TestListConfig_ReturnsAllKeys(t *testing.T) {
	dir := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", dir)
	defer os.Setenv("HOME", origHome)

	// Clear env vars.
	for _, key := range []string{
		"CLAUDE_AUTOPILOT_SKIP_PERMISSIONS",
		"CLAUDE_AUTOPILOT_HANG_TIMEOUT",
		"CLAUDE_AUTOPILOT_WEBHOOK_URL",
		"CLAUDE_AUTOPILOT_NOTIFICATION_DESKTOP",
		"CLAUDE_AUTOPILOT_NOTIFICATION_BELL",
	} {
		os.Unsetenv(key)
	}

	result, err := ListConfig()
	if err != nil {
		t.Fatalf("ListConfig: %v", err)
	}

	expectedKeys := []string{
		"skip_permissions",
		"hang_timeout",
		"webhook_url",
		"notification_desktop",
		"notification_bell",
	}

	for _, k := range expectedKeys {
		if _, ok := result[k]; !ok {
			t.Errorf("ListConfig missing key %q", k)
		}
	}

	if len(result) != len(expectedKeys) {
		t.Errorf("ListConfig returned %d keys; want %d", len(result), len(expectedKeys))
	}
}
