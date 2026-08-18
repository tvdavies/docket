// Package handlers delivers docket events to executable consumers declared in
// .docket/config.yaml. Each handler owns a durable cursor: matching events are
// delivered at least once, in log order, and the cursor advances only after the
// executable exits successfully.
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
	"strconv"
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

// Options controls handler execution. Zero values use safe defaults.
type Options struct {
	// Output receives handler stdout and stderr. It is normally the parent
	// command's stderr so handler logs cannot corrupt --json output.
	Output io.Writer
	// Timeout bounds one handler invocation. The default is 30 seconds;
	// handlers should enqueue or spawn long-running work, not become it.
	Timeout time.Duration
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
		progressed := false
		for _, name := range ws.Config.HandlerNames() {
			if failed[name] {
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

	if handlersPending(ws, failed) {
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
		batch, end, err := events.ReadBatch(ws, cursor)
		if err != nil {
			return err
		}
		if end <= cursor {
			return nil
		}

		matched := make([]events.Event, 0, len(batch))
		for _, event := range batch {
			if cfg.Matches(event.Type) {
				matched = append(matched, event)
			}
		}
		if len(matched) > 0 {
			if err := execute(ws, name, cfg.Run, matched, opts); err != nil {
				return err
			}
		}
		if err := advanceCursor(ws, name, end); err != nil {
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
	return advanced, store.WithLock(lockPath, drain)
}

func execute(ws *workspace.Workspace, name, run string, batch []events.Event, opts Options) error {
	var input bytes.Buffer
	enc := json.NewEncoder(&input)
	enc.SetEscapeHTML(false)
	for _, event := range batch {
		if err := enc.Encode(event); err != nil {
			return fmt.Errorf("encode event: %w", err)
		}
	}

	projectRoot := filepath.Dir(ws.Root)
	program := run
	if !filepath.IsAbs(program) {
		program = filepath.Join(projectRoot, program)
	}

	ctx, cancel := context.WithTimeout(context.Background(), opts.Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, program)
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
	cmd.Env = withEnv(os.Environ(), map[string]string{
		"DOCKET_HOME":          ws.Root,
		"DOCKET_ACTOR":         "handler:" + name,
		"DOCKET_HANDLER":       name,
		"DOCKET_HANDLER_STACK": appendHandler(os.Getenv("DOCKET_HANDLER_STACK"), name),
	})

	err := cmd.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("%s timed out after %s", run, opts.Timeout)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", run, err)
	}
	return nil
}

// Cursor returns a handler's delivery position. Handler cursors live outside
// the actor inbox namespace so commands such as `docket inbox --mark-read`
// cannot accidentally acknowledge handler delivery.
func Cursor(ws *workspace.Workspace, name string) int {
	data, err := os.ReadFile(handlerCursorFile(ws, name))
	if err != nil {
		return 0
	}
	position, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0
	}
	return position
}

func advanceCursor(ws *workspace.Workspace, name string, position int) error {
	return store.WriteAtomic(handlerCursorFile(ws, name), []byte(strconv.Itoa(position)+"\n"), 0o644)
}

func handlerCursorFile(ws *workspace.Workspace, name string) string {
	return filepath.Join(ws.HandlerStateDir(), name+".cursor")
}

func handlersPending(ws *workspace.Workspace, failed map[string]bool) bool {
	end := events.Count(ws)
	for _, name := range ws.Config.HandlerNames() {
		if !failed[name] && Cursor(ws, name) < end {
			return true
		}
	}
	return false
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
