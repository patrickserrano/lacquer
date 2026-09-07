package config

import (
	"strings"
	"testing"
)

// A retired secret must carry a reason. A bare name would be a silent opt-out
// of the secret-drop guard, which is precisely what that guard exists to
// prevent — so this is the assertion that keeps the escape hatch honest.
func TestRetiredSecretRequiresAReason(t *testing.T) {
	var r RetiredSecret
	err := r.UnmarshalTOML(map[string]any{"name": "SANITY_API_READ_TOKEN"})
	if err == nil {
		t.Fatal("a retired secret with no reason was accepted — the guard's escape hatch is now a silent opt-out")
	}
	if !strings.Contains(err.Error(), "reason") {
		t.Errorf("error should name the missing reason, got %q", err)
	}

	if err := r.UnmarshalTOML(map[string]any{"name": "X", "reason": "   "}); err == nil {
		t.Error("a whitespace-only reason was accepted")
	}
	if err := r.UnmarshalTOML(map[string]any{"reason": "no name"}); err == nil {
		t.Error("an entry with no name was accepted")
	}
	if err := r.UnmarshalTOML(map[string]any{"name": "X", "reasn": "typo"}); err == nil {
		t.Error("a typo'd key was silently dropped, turning an attributed retirement into an unattributed one")
	}
	if err := r.UnmarshalTOML("SANITY_API_READ_TOKEN"); err == nil {
		t.Error("the bare-string form was accepted — it is deliberately not supported, because a shorthand is what would make dropping a credential easy")
	}

	if err := r.UnmarshalTOML(map[string]any{
		"name": "SANITY_API_READ_TOKEN", "reason": "migrated off Sanity to Payload CMS",
	}); err != nil {
		t.Fatalf("a properly attributed retirement was rejected: %v", err)
	}
	if r.Name != "SANITY_API_READ_TOKEN" || r.Reason == "" {
		t.Errorf("parsed = %+v", r)
	}
}
