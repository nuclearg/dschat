package deepseek

import (
	"encoding/json"
	"testing"
)

func buildBodyForTest(t *testing.T, req Request) map[string]any {
	t.Helper()
	body, err := (&Client{}).buildBody(req)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestBuildBodyIncludesSafetyCheckWhenEnabled(t *testing.T) {
	body := buildBodyForTest(t, Request{Model: "deepseek-flash", SafetyCheck: true})
	got, ok := body["safety_check"].(map[string]any)
	if !ok {
		t.Fatalf("safety_check missing: %v", body["safety_check"])
	}
	if got["level"] != "high" {
		t.Fatalf("safety_check.level = %v, want high", got["level"])
	}
}

func TestBuildBodyOmitsSafetyCheckWhenDisabled(t *testing.T) {
	body := buildBodyForTest(t, Request{Model: "deepseek-flash", SafetyCheck: false})
	if _, ok := body["safety_check"]; ok {
		t.Fatalf("safety_check should be omitted, got %v", body["safety_check"])
	}
}
