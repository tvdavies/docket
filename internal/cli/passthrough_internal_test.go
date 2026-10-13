package cli

import (
	"strings"
	"testing"
)

func TestBuiltinAliasesAreReservedFromPluginPassthrough(t *testing.T) {
	for _, name := range []string{"reference", "ref", "skill", "guide"} {
		if !builtinCommand(name) {
			t.Fatalf("builtin command or alias %q is not reserved", name)
		}
	}
}

func TestLeadingWorkspaceFlagIsStrippedBeforePluginName(t *testing.T) {
	for _, test := range []struct {
		args     []string
		selector string
		rest     string
	}{
		{[]string{"-w", "alpha", "dispatch", "go"}, "alpha", "dispatch go"},
		{[]string{"--workspace", "~/dev/a", "dispatch"}, "~/dev/a", "dispatch"},
		{[]string{"--workspace=alpha", "dispatch"}, "alpha", "dispatch"},
		{[]string{"-walpha", "dispatch"}, "alpha", "dispatch"},
		{[]string{"-w=alpha", "dispatch"}, "alpha", "dispatch"},
		{[]string{"dispatch", "-w", "alpha"}, "", "dispatch -w alpha"},
		{[]string{"-w"}, "", "-w"},
	} {
		selector, rest := leadingWorkspaceFlag(test.args)
		if selector != test.selector || strings.Join(rest, " ") != test.rest {
			t.Fatalf("leadingWorkspaceFlag(%q) = %q, %q", test.args, selector, rest)
		}
	}
}
