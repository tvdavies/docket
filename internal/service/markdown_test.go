package service

import (
	"strings"
	"testing"
)

func TestRenderMarkdownSupportsGFMAndBlocksExecutableHTML(t *testing.T) {
	source := "# Heading\n\n- [x] done\n\n```go\nfmt.Println(1)\n```\n\n<script>alert(1)</script>\n\n[bad](javascript:alert(1))"
	rendered, err := renderMarkdown(source)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"<h1>Heading</h1>", "<input checked=\"\" disabled=\"\" type=\"checkbox\">", "<pre><code class=\"language-go\""} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered markdown missing %q: %s", want, rendered)
		}
	}
	lower := strings.ToLower(rendered)
	for _, unsafe := range []string{"<script", "javascript:", "onclick="} {
		if strings.Contains(lower, unsafe) {
			t.Fatalf("rendered markdown contains unsafe %q: %s", unsafe, rendered)
		}
	}
}
