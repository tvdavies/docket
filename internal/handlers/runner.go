// Package handlers delivers docket events to executable or Lua consumers
// declared in .docket/config.yaml. Each handler owns a durable cursor: matching
// events are delivered at least once, in log order, and the cursor advances
// only after the isolated invocation exits successfully.
package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/tvdavies/docket/internal/events"
	"github.com/tvdavies/docket/internal/store"
	"github.com/tvdavies/docket/internal/workspace"
)

const (
	defaultTimeout = 30 * time.Second
	maxDrainRounds = 32
)

// Scope controls which delivery classes a drain executes.
type Scope uint8

const (
	// ScopeAll runs inline and service-delivered handlers. The persistent
	// service uses this to drain every durable cursor.
	ScopeAll Scope = iota
	// ScopeInline skips handlers declared with delivery: service. Mutating CLI
	// commands use this so they return without waiting for asynchronous work.
	ScopeInline
)

// Options controls handler execution. Zero values use safe defaults.
type Options struct {
	// Context cancels lock waits and the entire handler process group. A nil
	// context means background execution bounded only by Timeout.
	Context context.Context
	// Scope defaults to ScopeAll.
	Scope Scope
	// Output receives handler stdout and stderr. It is normally the parent
	// command's stderr so handler logs cannot corrupt --json output.
	Output io.Writer
	// Timeout bounds one handler invocation. The default is 30 seconds;
	// handlers should enqueue or spawn long-running work, not become it.
	Timeout time.Duration
	// LuaCommand overrides the child runner command before the script argument.
	// Production uses the current Docket executable plus __lua-hook; tests may
	// supply an isolated helper process.
	LuaCommand []string
}

// Failure records one handler that could not drain. The task mutation and its
// event are already durable; callers should report failures as warnings rather
// than implying the mutation was rolled back.
type Failure struct {
	Handler string
	Err     error
}

func (f Failure) Error() string { return fmt.Sprintf("handler %q: %v", f.Handler, f.Err) }

// DrainAll brings every configured handler cursor up to the current end of the
// log. It repeats when handlers append further events, allowing short event
// chains to settle synchronously. Failed handlers are not retried until the
// next drain; their cursors remain before the failed batch.
func DrainAll(ws *workspace.Workspace, opts Options) []Failure {
	if len(ws.Config.Handlers) == 0 {
		return nil
	}
	if opts.Context == nil {
		opts.Context = context.Background()
	}
	if opts.Output == nil {
		opts.Output = io.Discard
	}
	if opts.Timeout <= 0 {
		opts.Timeout = defaultTimeout
	}

	failed := map[string]bool{}
	nested := os.Getenv("DOCKET_HANDLER_STACK") != ""
	var failures []Failure
	for round := 0; round < maxDrainRounds; round++ {
		if err := opts.Context.Err(); err != nil {
			failures = append(failures, Failure{Handler: "runner", Err: err})
			return failures
		}
		progressed := false
		for _, name := range ws.Config.HandlerNames() {
			if failed[name] || !runsInScope(ws.Config.Handlers[name], opts.Scope) {
				continue
			}
			advanced, err := drainOne(ws, name, ws.Config.Handlers[name], opts, nested)
			if err != nil {
				failed[name] = true
				failures = append(failures, Failure{Handler: name, Err: err})
				continue
			}
			progressed = progressed || advanced
		}
		if !progressed {
			return failures
		}
	}

	if handlersPending(ws, failed, opts.Scope) {
		failures = append(failures, Failure{
			Handler: "runner",
			Err:     fmt.Errorf("event chain did not settle after %d rounds; remaining events will retry on the next drain", maxDrainRounds),
		})
	}
	return failures
}

func drainOne(ws *workspace.Workspace, name string, cfg workspace.HandlerConfig, opts Options, nested bool) (bool, error) {
	lockPath := filepath.Join(ws.HandlerStateDir(), name+".lock")
	advanced := false
	drain := func() error {
		cursor := Cursor(ws, name)
		batch, end, checkpoint, err := events.ReadBatchCheckpoint(ws, cursor)
		if err != nil {
			return err
		}
		if end <= cursor {
			return nil
		}

		matched := make([]events.Event, 0, len(batch))
		for _, event := range batch {
			if matchesEvent(cfg, event) {
				matched = append(matched, event)
			}
		}
		if len(matched) > 0 {
			if err := execute(ws, name, cfg, matched, opts); err != nil {
				return err
			}
		}
		if err := advanceCursor(ws, name, end, checkpoint); err != nil {
			return err
		}
		advanced = true
		return nil
	}

	// A handler-triggered docket command must never block on any handler lock:
	// two concurrent handlers could otherwise each hold one lock while their
	// nested command waits for the other. The outer top-level drains settle all
	// events after those locks release. Unrelated top-level drains still block.
	if nested {
		acquired, err := store.TryWithLock(lockPath, drain)
		if !acquired {
			return false, err
		}
		return advanced, err
	}
	return advanced, store.WithLockContext(opts.Context, lockPath, drain)
}

func execute(ws *workspace.Workspace, name string, config workspace.HandlerConfig, batch []events.Event, opts Options) error {
	// A Lua child receives one event. Besides making handle(event, docket) the
	// natural unit of isolation, this prevents os.exit(0) in one invocation from
	// acknowledging later events it never saw. Executable handlers retain their
	// original batched JSONL contract.
	if config.Lua != "" && len(batch) > 1 {
		for _, event := range batch {
			if err := execute(ws, name, config, []events.Event{event}, opts); err != nil {
				return err
			}
		}
		return nil
	}

	var input bytes.Buffer
	enc := json.NewEncoder(&input)
	enc.SetEscapeHTML(false)
	for _, event := range batch {
		if err := enc.Encode(event); err != nil {
			return fmt.Errorf("encode event: %w", err)
		}
	}

	projectRoot := filepath.Dir(ws.Root)
	program, args, display, err := handlerCommand(projectRoot, config, opts)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(opts.Context, opts.Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, program, args...)
	// Handlers are scripts and may launch children. Put the invocation in its
	// own process group so a timeout terminates the whole tree, not just the
	// parent shell while a child keeps the command waiting.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.Dir = projectRoot
	cmd.Stdin = &input
	cmd.Stdout = opts.Output
	cmd.Stderr = opts.Output
	environment := map[string]string{
		"DOCKET_HOME":          ws.Root,
		"DOCKET_ACTOR":         "handler:" + name,
		"DOCKET_HANDLER":       name,
		"DOCKET_HANDLER_STACK": appendHandler(os.Getenv("DOCKET_HANDLER_STACK"), name),
		// Clear plugin context inherited through nested docket commands before
		// selectively setting the current handler's own plugin.
		"DOCKET_PLUGIN":        "",
		"DOCKET_PLUGIN_ROOT":   "",
		"DOCKET_PLUGIN_CONFIG": "",
	}
	if config.PluginName != "" {
		payload, err := json.Marshal(map[string]any{
			"config": config.PluginConfig, "status_config": config.PluginStatusConfig,
		})
		if err != nil {
			return fmt.Errorf("encode plugin config: %w", err)
		}
		environment["DOCKET_PLUGIN"] = config.PluginName
		environment["DOCKET_PLUGIN_ROOT"] = config.PluginRoot
		environment["DOCKET_PLUGIN_CONFIG"] = string(payload)
	}
	cmd.Env = withEnv(os.Environ(), environment)

	err = cmd.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("%s timed out after %s", display, opts.Timeout)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", display, err)
	}
	return nil
}

func handlerCommand(projectRoot string, config workspace.HandlerConfig, opts Options) (string, []string, string, error) {
	base := projectRoot
	if config.PluginRoot != "" {
		base = config.PluginRoot
	}
	if config.Lua != "" {
		script := config.Lua
		if !filepath.IsAbs(script) {
			script = filepath.Join(base, script)
		}
		command := append([]string(nil), opts.LuaCommand...)
		if len(command) == 0 {
			executable, err := os.Executable()
			if err != nil {
				return "", nil, "", fmt.Errorf("resolve Docket executable for Lua handler: %w", err)
			}
			command = []string{executable, "__lua-hook"}
		}
		return command[0], append(command[1:], script), config.Lua, nil
	}

	program := config.Run
	if !filepath.IsAbs(program) {
		program = filepath.Join(base, program)
	}
	return program, nil, config.Run, nil
}

// Cursor returns a handler's delivery position. Handler cursors live outside
// the actor inbox namespace so commands such as `docket inbox --mark-read`
// cannot accidentally acknowledge handler delivery.
func Cursor(ws *workspace.Workspace, name string) int {
	data, err := os.ReadFile(handlerCursorFile(ws, name))
	if err != nil {
		return 0
	}
	var state cursorState
	if err := json.Unmarshal(data, &state); err != nil {
		// v0.2 cursors were unverifiable plain line counts. Replay once rather
		// than risk silently skipping rewritten history; successful delivery
		// upgrades the cursor to a checkpointed JSON record.
		return 0
	}
	if state.Position <= 0 {
		return 0
	}
	prefixHash, found, err := events.PrefixHash(ws, state.Position)
	if err != nil || found < state.Position || prefixHash != state.PrefixHash {
		return 0
	}
	return state.Position
}

// SeedCursorAtEnd creates a missing handler cursor at the current log end.
// Existing cursors are preserved so enablement and hot reload never replay.
func SeedCursorAtEnd(ws *workspace.Workspace, name string) error {
	path := handlerCursorFile(ws, name)
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	position := events.Count(ws)
	hash, found, err := events.PrefixHash(ws, position)
	if err != nil {
		return err
	}
	if found < position {
		return fmt.Errorf("event log shortened while seeding handler %q cursor", name)
	}
	return writeCursor(path, position, hash)
}

// ResetCursor writes an explicit zero checkpoint so the next drain replays
// from the beginning while service hot-reload seeding can distinguish this
// opt-in from an accidentally missing new-handler cursor.
func ResetCursor(ws *workspace.Workspace, name string) error {
	return writeCursor(handlerCursorFile(ws, name), 0, "")
}

// AdoptCursor copies a legacy handler checkpoint to a namespaced identity. The
// source is deliberately retained for atomic rollback to legacy wiring.
func AdoptCursor(ws *workspace.Workspace, legacy, identity string) error {
	source := handlerCursorFile(ws, legacy)
	data, err := os.ReadFile(source)
	if err != nil {
		return fmt.Errorf("read legacy handler %q cursor: %w", legacy, err)
	}
	var state cursorState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("legacy handler %q cursor is not checkpointed JSON", legacy)
	}
	if state.Position > 0 {
		hash, found, err := events.PrefixHash(ws, state.Position)
		if err != nil || found < state.Position || hash != state.PrefixHash {
			return fmt.Errorf("legacy handler %q cursor does not match the current event log", legacy)
		}
	}
	return store.WriteAtomic(handlerCursorFile(ws, identity), data, 0o644)
}

func advanceCursor(ws *workspace.Workspace, name string, position int, expectedHash string) error {
	prefixHash, found, err := events.PrefixHash(ws, position)
	if err != nil {
		return err
	}
	if found < position {
		return fmt.Errorf("event log shortened while advancing handler %q cursor", name)
	}
	if prefixHash != expectedHash {
		return fmt.Errorf("event log changed while handler %q was running; batch will replay", name)
	}
	return writeCursor(handlerCursorFile(ws, name), position, expectedHash)
}

func writeCursor(path string, position int, prefixHash string) error {
	data, err := json.Marshal(cursorState{Position: position, PrefixHash: prefixHash})
	if err != nil {
		return err
	}
	return store.WriteAtomic(path, append(data, '\n'), 0o644)
}

// cursorState ties a line position to the exact log prefix it acknowledged.
// If git or another process replaces history, Cursor resets to replay.
type cursorState struct {
	Position   int    `json:"position"`
	PrefixHash string `json:"prefix_hash"`
}

func handlerCursorFile(ws *workspace.Workspace, name string) string {
	return filepath.Join(ws.HandlerStateDir(), name+".cursor")
}

func handlersPending(ws *workspace.Workspace, failed map[string]bool, scope Scope) bool {
	end := events.Count(ws)
	for _, name := range ws.Config.HandlerNames() {
		if !failed[name] && runsInScope(ws.Config.Handlers[name], scope) && Cursor(ws, name) < end {
			return true
		}
	}
	return false
}

func runsInScope(config workspace.HandlerConfig, scope Scope) bool {
	return scope != ScopeInline || config.Delivery != "service"
}

func appendHandler(stack, name string) string {
	if stack == "" {
		return name
	}
	return stack + "," + name
}

func withEnv(existing []string, values map[string]string) []string {
	out := make([]string, 0, len(existing)+len(values))
	for _, entry := range existing {
		key, _, ok := strings.Cut(entry, "=")
		if ok {
			if _, replace := values[key]; replace {
				continue
			}
		}
		out = append(out, entry)
	}
	for key, value := range values {
		out = append(out, key+"="+value)
	}
	return out
}
