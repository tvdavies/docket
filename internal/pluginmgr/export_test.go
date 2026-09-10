package pluginmgr

// SetCrashHook installs a fixture-only hook that fires at each commit boundary
// of Handoff. Production never sets one.
func SetCrashHook(fn func(stage string)) { crashHook = fn }
