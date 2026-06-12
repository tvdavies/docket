package store

import "testing"

func TestFrontmatterRoundTrip(t *testing.T) {
	type meta struct {
		ID    string   `yaml:"id"`
		Tags  []string `yaml:"tags"`
		Count int      `yaml:"count"`
	}
	in := meta{ID: "TASK-0007", Tags: []string{"bug", "auth"}, Count: 3}
	body := "Line one.\n\n## Section\nLine two."

	doc, err := RenderFrontmatter(in, body)
	if err != nil {
		t.Fatal(err)
	}

	var out meta
	gotBody, err := ParseFrontmatter(doc, &out)
	if err != nil {
		t.Fatal(err)
	}
	if out.ID != in.ID || out.Count != in.Count || len(out.Tags) != 2 {
		t.Fatalf("meta mismatch: %+v", out)
	}
	if gotBody != body+"\n" && gotBody != body {
		t.Fatalf("body mismatch: %q", gotBody)
	}
}

func TestSplitNoFrontmatter(t *testing.T) {
	meta, body := SplitFrontmatter([]byte("just body, no fence"))
	if len(meta) != 0 {
		t.Fatalf("expected no meta, got %q", meta)
	}
	if string(body) != "just body, no fence" {
		t.Fatalf("body mismatch: %q", body)
	}
}

func TestParseIDNumber(t *testing.T) {
	n, ok := ParseIDNumber("TASK", "TASK-0042")
	if !ok || n != 42 {
		t.Fatalf("got %d %v", n, ok)
	}
	if _, ok := ParseIDNumber("TASK", "PROJ-0001"); ok {
		t.Fatal("expected mismatch")
	}
}
