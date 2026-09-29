package widget

import (
	"encoding/json"
	"os"
	"testing"
)

func TestPublicWireFixture(t *testing.T) {
	raw, err := os.ReadFile("../../packages/plugin-ui/fixtures/wire.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Workspace string          `json:"workspace"`
		Record    Record          `json:"record"`
		Preview   json.RawMessage `json:"preview"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if err := Validate(fixture.Record, fixture.Workspace); err != nil {
		t.Fatal(err)
	}
	if _, err := ParsePreview(fixture.Preview); err != nil {
		t.Fatal(err)
	}
}
func TestSafeDestinations(t *testing.T) {
	for _, url := range []string{"//evil.test/run", "https://evil.test/run", "/plugins/other/run", "/plugins/fixture/../other", "/plugins/fixture/%2e%2e/other", "/workspaces/other/tasks/TASK-1", "javascript:alert(1)", "file:///etc/passwd"} {
		if SafeURL(url, "session", "fixture", "one") {
			t.Fatal("accepted", url)
		}
	}
	if !SafeURL("/plugins/fixture/sessions/one", "session", "fixture", "one") || !SafeURL("https://example.test/plan", "plan", "fixture", "one") {
		t.Fatal("safe URL rejected")
	}
}
