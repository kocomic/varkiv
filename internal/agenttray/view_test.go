package agenttray

import (
	"strings"
	"testing"

	"varkiv/internal/deviceagent"
)

func TestNormalizeLocale(t *testing.T) {
	tests := map[string]string{"zh-CN": "zh-CN", "zh-Hant-HK": "zh-TW", "zh-MO": "zh-TW", "ja-JP": "ja", "en-US": "en", "de-DE": "en"}
	for input, expected := range tests {
		if actual := normalizeLocale(input); actual != expected {
			t.Fatalf("normalizeLocale(%q)=%q want %q", input, actual, expected)
		}
	}
}

func TestStatusTextIsLocalizedAndPrivacyMinimized(t *testing.T) {
	status := &deviceagent.AgentSyncStatus{State: "complete", Uploaded: 2, Downloaded: 1}
	for _, locale := range []string{"zh-CN", "zh-TW", "ja", "en"} {
		text := textForLocale(locale)
		result := statusText(text, status)
		if result == "" || !strings.Contains(result, "2") || !strings.Contains(result, "1") {
			t.Fatalf("locale %s produced incomplete status %q", locale, result)
		}
		for _, private := range []string{"http://", "https://", "token", "\\", "/" + "Users/"} {
			if strings.Contains(result, private) {
				t.Fatalf("locale %s exposed private marker %q in %q", locale, private, result)
			}
		}
	}
	if got := statusText(textForLocale("en"), &deviceagent.AgentSyncStatus{State: "conflict", Conflicts: 1}); got != "Conflict needs review" {
		t.Fatalf("conflict status=%q", got)
	}
}
