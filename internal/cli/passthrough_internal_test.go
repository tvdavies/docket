package cli

import "testing"

func TestBuiltinAliasesAreReservedFromPluginPassthrough(t *testing.T) {
	for _, name := range []string{"reference", "ref", "skill", "guide"} {
		if !builtinCommand(name) {
			t.Fatalf("builtin command or alias %q is not reserved", name)
		}
	}
}
