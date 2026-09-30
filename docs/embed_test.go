package docs

import (
	"strings"
	"testing"
)

func TestTopicsIncludeTheReferenceGuides(t *testing.T) {
	for _, name := range []string{"plugins", "plugins/authoring", "cli", "inbox", "lua-hooks", "agent-guide"} {
		topic, content, ok := Read(name)
		if !ok || strings.TrimSpace(content) == "" || topic.Title == "" {
			t.Fatalf("topic %q: ok=%v title=%q", name, ok, topic.Title)
		}
	}
	if topic, _, ok := Read("docs/plugins/authoring.md"); !ok || topic.Name != "plugins/authoring" {
		t.Fatalf("path form did not resolve: %+v", topic)
	}
	for _, removed := range []string{"missing", "plugins/ui", "plugin-sdk.d.ts", "web-interface"} {
		if _, _, ok := Read(removed); ok {
			t.Fatalf("topic %q resolved", removed)
		}
	}
}
