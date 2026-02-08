package compat

import "testing"

// ---------------------------------------------------------------------------
// LookupCompat
// ---------------------------------------------------------------------------

func TestLookupCompat_V2(t *testing.T) {
	entry, err := LookupCompat("2.1.0")
	if err != nil {
		t.Fatalf("LookupCompat(\"2.1.0\"): %v", err)
	}
	if entry == nil {
		t.Fatal("expected non-nil entry for v2.1.0")
	}
	if entry.MinVersion != "2.0.0" {
		t.Errorf("MinVersion = %q; want 2.0.0", entry.MinVersion)
	}
	if !entry.StreamJSON {
		t.Error("v2 should support StreamJSON")
	}
	if !entry.ResumeFlag {
		t.Error("v2 should support ResumeFlag")
	}
	if entry.ExitCodeRateLimit != 75 {
		t.Errorf("ExitCodeRateLimit = %d; want 75", entry.ExitCodeRateLimit)
	}
}

func TestLookupCompat_V1(t *testing.T) {
	entry, err := LookupCompat("1.5.0")
	if err != nil {
		t.Fatalf("LookupCompat(\"1.5.0\"): %v", err)
	}
	if entry == nil {
		t.Fatal("expected non-nil entry for v1.5.0")
	}
	if entry.MinVersion != "1.0.0" {
		t.Errorf("MinVersion = %q; want 1.0.0", entry.MinVersion)
	}
	if entry.StreamJSON {
		t.Error("v1 should not support StreamJSON")
	}
	if entry.ResumeFlag {
		t.Error("v1 should not support ResumeFlag")
	}
	if entry.ExitCodeRateLimit != -1 {
		t.Errorf("ExitCodeRateLimit = %d; want -1", entry.ExitCodeRateLimit)
	}
}

func TestLookupCompat_Unknown(t *testing.T) {
	entry, err := LookupCompat("3.0.0")
	if err != nil {
		t.Fatalf("LookupCompat(\"3.0.0\"): %v", err)
	}
	if entry != nil {
		t.Errorf("expected nil for unknown version 3.0.0, got %+v", entry)
	}
}

func TestLookupCompat_V2Boundary(t *testing.T) {
	// Test exact boundaries.
	entry, err := LookupCompat("2.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if entry == nil {
		t.Fatal("2.0.0 should match v2 entry")
	}

	entry, err = LookupCompat("2.99.99")
	if err != nil {
		t.Fatal(err)
	}
	if entry == nil {
		t.Fatal("2.99.99 should match v2 entry")
	}
}

func TestLookupCompat_V1Boundary(t *testing.T) {
	entry, err := LookupCompat("1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if entry == nil {
		t.Fatal("1.0.0 should match v1 entry")
	}

	entry, err = LookupCompat("1.99.99")
	if err != nil {
		t.Fatal(err)
	}
	if entry == nil {
		t.Fatal("1.99.99 should match v1 entry")
	}
}

// ---------------------------------------------------------------------------
// CompareSemver
// ---------------------------------------------------------------------------

func TestCompareSemver(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.0.0", "2.0.0", -1},
		{"2.0.0", "1.0.0", 1},
		{"1.1.0", "1.0.0", 1},
		{"1.0.1", "1.0.0", 1},
		{"1.0.0", "1.0.1", -1},
		{"2.1.3", "2.1.3", 0},
		{"1.99.99", "2.0.0", -1},
		{"10.0.0", "9.99.99", 1},
		// With v prefix.
		{"v2.1.0", "2.1.0", 0},
		// With pre-release suffix (ignored).
		{"2.1.0-beta", "2.1.0", 0},
		// With build metadata (ignored).
		{"2.1.0+build123", "2.1.0", 0},
	}

	for _, tt := range tests {
		got := CompareSemver(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("CompareSemver(%q, %q) = %d; want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// NewAdapter with known entry
// ---------------------------------------------------------------------------

func TestNewAdapter_KnownEntry(t *testing.T) {
	entry := &CompatEntry{
		MinVersion:        "2.0.0",
		MaxVersion:        "2.99.99",
		StreamJSON:        true,
		ResumeFlag:        true,
		ExitCodeRateLimit: 75,
	}

	adapter := NewAdapter(entry)

	if !adapter.SupportsStreamJSON() {
		t.Error("adapter should support stream JSON for v2")
	}
	if !adapter.SupportsResume() {
		t.Error("adapter should support resume for v2")
	}
	if adapter.RateLimitExitCode() != 75 {
		t.Errorf("RateLimitExitCode = %d; want 75", adapter.RateLimitExitCode())
	}
}

func TestNewAdapter_KnownEntry_BuildArgs(t *testing.T) {
	entry := &CompatEntry{
		StreamJSON: true,
		ResumeFlag: true,
	}
	adapter := NewAdapter(entry)

	args := adapter.BuildArgs("do stuff", "opus", "session-123", true, []string{"--verbose"})

	assertContains(t, args, "--print")
	assertContains(t, args, "--output-format")
	assertContains(t, args, "stream-json")
	assertContains(t, args, "--resume")
	assertContains(t, args, "session-123")
	assertContains(t, args, "--model")
	assertContains(t, args, "opus")
	assertContains(t, args, "--dangerously-skip-permissions")
	assertContains(t, args, "--verbose")
	assertContains(t, args, "--")
	assertContains(t, args, "do stuff")
	assertNotContains(t, args, "--session-id")
	assertNotContains(t, args, "--prompt")

	// Verify "--" separator comes before the prompt.
	dashIdx, promptIdx := -1, -1
	for i, a := range args {
		if a == "--" {
			dashIdx = i
		}
		if a == "do stuff" {
			promptIdx = i
		}
	}
	if dashIdx == -1 || promptIdx == -1 || dashIdx >= promptIdx {
		t.Errorf("expected '--' before prompt in args: %v", args)
	}
}

func TestNewAdapter_KnownEntry_NoResume(t *testing.T) {
	entry := &CompatEntry{
		StreamJSON: false,
		ResumeFlag: false,
	}
	adapter := NewAdapter(entry)

	args := adapter.BuildArgs("prompt", "", "", false, nil)

	assertContains(t, args, "--print")
	assertNotContains(t, args, "--output-format")
	assertNotContains(t, args, "--resume")
	assertNotContains(t, args, "--model")
	assertNotContains(t, args, "--dangerously-skip-permissions")
}

// ---------------------------------------------------------------------------
// NewAdapter with nil entry (safe mode)
// ---------------------------------------------------------------------------

func TestNewAdapter_NilEntry_SafeMode(t *testing.T) {
	adapter := NewAdapter(nil)

	// Safe mode optimistically supports modern features.
	if !adapter.SupportsStreamJSON() {
		t.Error("safe mode should support stream JSON")
	}
	if !adapter.SupportsResume() {
		t.Error("safe mode should support resume")
	}
	if adapter.RateLimitExitCode() != 75 {
		t.Errorf("safe mode RateLimitExitCode = %d; want 75", adapter.RateLimitExitCode())
	}
}

func TestNewAdapter_NilEntry_BuildArgs(t *testing.T) {
	adapter := NewAdapter(nil)
	args := adapter.BuildArgs("test prompt", "", "sess-1", false, nil)

	assertContains(t, args, "--print")
	assertContains(t, args, "--output-format")
	assertContains(t, args, "stream-json")
	assertContains(t, args, "--resume")
	assertContains(t, args, "sess-1")
	assertContains(t, args, "test prompt")
	assertNotContains(t, args, "--session-id")
	assertNotContains(t, args, "--prompt")
}

// ---------------------------------------------------------------------------
// parseVersionOutput
// ---------------------------------------------------------------------------

func TestParseVersionOutput(t *testing.T) {
	tests := []struct {
		input   string
		want    string
		wantErr bool
	}{
		{"claude 2.1.3", "2.1.3", false},
		{"Claude CLI v2.0.0", "2.0.0", false},
		{"v1.5.2", "1.5.2", false},
		{"no version here", "", true},
		{"", "", true},
	}

	for _, tt := range tests {
		got, err := parseVersionOutput(tt.input)
		if tt.wantErr {
			if err == nil {
				t.Errorf("parseVersionOutput(%q) expected error, got %q", tt.input, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseVersionOutput(%q) error: %v", tt.input, err)
			continue
		}
		if got != tt.want {
			t.Errorf("parseVersionOutput(%q) = %q; want %q", tt.input, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func assertContains(t *testing.T, args []string, want string) {
	t.Helper()
	for _, a := range args {
		if a == want {
			return
		}
	}
	t.Errorf("args %v does not contain %q", args, want)
}

func assertNotContains(t *testing.T, args []string, unwanted string) {
	t.Helper()
	for _, a := range args {
		if a == unwanted {
			t.Errorf("args %v should not contain %q", args, unwanted)
			return
		}
	}
}
