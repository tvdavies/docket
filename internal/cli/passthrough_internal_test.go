package cli

import "testing"

func TestBuiltinAliasesAreReservedFromPluginPassthrough(t *testing.T) {
	for _, name := range []string{"reference", "ref", "skill", "guide", "__lua-hook", "help", "completion", "events", "link"} {
		if !builtinCommand(name) {
			t.Fatalf("builtin command or alias %q is not reserved", name)
		}
	}
	// Removed commands are no longer reserved.
	for _, name := range []string{"serve", "watch", "files", "unlink", "reindex", "attach", "detach", "current", "context"} {
		if builtinCommand(name) {
			t.Fatalf("removed command %q is still reserved", name)
		}
	}
}
