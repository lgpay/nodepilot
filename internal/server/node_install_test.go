package server

import "testing"

func TestShellQuote(t *testing.T) {
	got := shellQuote("https://example.test/a'b; echo unsafe")
	want := "'https://example.test/a'\"'\"'b; echo unsafe'"
	if got != want {
		t.Fatalf("shellQuote() = %q, want %q", got, want)
	}
}

func TestValidatePanelURL(t *testing.T) {
	for _, raw := range []string{"https://panel.example", "http://127.0.0.1:8080"} {
		if err := validatePanelURL(raw); err != nil {
			t.Errorf("validatePanelURL(%q) returned error: %v", raw, err)
		}
	}
	for _, raw := range []string{"panel.example", "javascript:alert(1)", "https://user:pass@panel.example", "https://"} {
		if err := validatePanelURL(raw); err == nil {
			t.Errorf("validatePanelURL(%q) accepted invalid URL", raw)
		}
	}
}
