// Package cli wires docket's command surface (cobra) over the internal store
// packages. Every command supports --json for stable machine output; the
// default is concise human-readable text.
package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tvdavies/docket/internal/events"
	"github.com/tvdavies/docket/internal/handlers"
	"github.com/tvdavies/docket/internal/session"
	"github.com/tvdavies/docket/internal/workspace"
)

// Version is set at build time via -ldflags.
var Version = "dev"

// Global flags.
var (
	flagJSON    bool
	flagSession string
)

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "docket",
		Short: "The docket that travels with the work — a file-backed task store that hands context between agent sessions",
		Long: `docket — the slip that travels with the job.

A lightweight, file-backed task store for agents. Tasks are plain markdown +
YAML on disk; an optional local service watches multiple workspaces and serves
status. An agent picks up a task,
does work, and hands full context to the next session by attaching to the task.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       Version,
	}
	root.PersistentFlags().BoolVar(&flagJSON, "json", false, "machine-readable JSON output")
	root.PersistentFlags().StringVar(&flagSession, "session", "", "session id for attach scoping (falls back to $DOCKET_SESSION)")

	root.AddCommand(
		newInitCmd(),
		newNewCmd(),
		newListCmd(),
		newShowCmd(),
		newContextCmd(),
		newEditCmd(),
		newMoveCmd(),
		newLabelCmd(),
		newCommentCmd(),
		newAttachFileCmd(),
		newFilesCmd(),
		newLinkCmd(),
		newUnlinkCmd(),
		newProjectCmd(),
		newAttachCmd(),
		newDetachCmd(),
		newCurrentCmd(),
		newInboxCmd(),
		newWatchCmd(),
		newEventsCmd(),
		newReindexCmd(),
		newWorkspaceCmd(),
		newServeCmd(),
		newServiceCmd(),
		newSkillCmd(),
	)
	return root
}

// Execute runs the CLI and returns a process exit code.
func Execute() int {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "docket: "+err.Error())
		return 1
	}
	return 0
}

// openWS discovers and opens the workspace.
func openWS() (*workspace.Workspace, error) {
	return workspace.Open()
}

// appendEvent durably records a mutation and then drains configured post-hoc
// handlers. A handler failure cannot roll back the mutation, so it is reported
// as a warning and left unread for the next drain. Handler ancestry is carried
// in the subprocess environment so recursive docket commands never block on
// handler locks; unrelated top-level drains wait and deliver.
func appendEvent(ws *workspace.Workspace, event events.Event) error {
	if err := events.Append(ws, event); err != nil {
		return fmt.Errorf("append event: %w", err)
	}
	for _, failure := range handlers.DrainAll(ws, handlers.Options{Scope: handlers.ScopeInline, Output: os.Stderr}) {
		fmt.Fprintf(os.Stderr, "docket: warning: %s\n", failure.Error())
	}
	return nil
}

// actor resolves the acting identity: $DOCKET_ACTOR → git user → "unknown".
func actor() string {
	if a := os.Getenv("DOCKET_ACTOR"); a != "" {
		return a
	}
	if out, err := exec.Command("git", "config", "user.name").Output(); err == nil {
		if name := strings.TrimSpace(string(out)); name != "" {
			return name
		}
	}
	return "unknown"
}

// sessionID resolves the caller's session id.
func sessionID() string {
	return session.Resolve(flagSession, os.Getenv("DOCKET_SESSION"))
}

// resolveTaskID returns the explicit id if given, else the task currently
// attached to this session.
func resolveTaskID(ws *workspace.Workspace, explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if id := session.Current(ws, sessionID()); id != "" {
		return id, nil
	}
	return "", fmt.Errorf("no task id given and no task attached to this session (use `docket attach <id>` first)")
}

// printJSON writes v as indented JSON to stdout.
func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// jsonIndent marshals v as indented JSON bytes.
func jsonIndent(v any) ([]byte, error) {
	return json.MarshalIndent(v, "", "  ")
}
