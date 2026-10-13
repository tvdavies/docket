package cli

import (
	"os"
	"testing"
)

func TestPagerCommandHonoursPAGER(t *testing.T) {
	t.Setenv("PAGER", "")
	os.Unsetenv("PAGER")
	if pager, ok := pagerCommand(); !ok || pager != "less -R" {
		t.Fatalf("default pager = %q, %v", pager, ok)
	}
	t.Setenv("PAGER", "most -s")
	if pager, ok := pagerCommand(); !ok || pager != "most -s" {
		t.Fatalf("custom pager = %q, %v", pager, ok)
	}
	for _, disabled := range []string{"", "  ", "cat"} {
		t.Setenv("PAGER", disabled)
		if _, ok := pagerCommand(); ok {
			t.Fatalf("PAGER=%q should disable paging", disabled)
		}
	}
}
