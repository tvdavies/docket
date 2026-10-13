// Package cli wires docket's command surface (cobra) over the internal store
// packages. Every command supports --json for stable machine output; the
// default is concise human-readable text.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tvdavies/docket/internal/events"
	"github.com/tvdavies/docket/internal/handlers"
	"github.com/tvdavies/docket/internal/plugin"
	"github.com/tvdavies/docket/internal/session"
	"github.com/tvdavies/docket/internal/workspace"
)

// Version is set at build time via -ldflags.
var Version = "dev"

// Global flags.
var (
	flagJSON    bool
	flagCompact bool
	flagSession string
)

const (
	groupTasks      = "tasks"
	groupContext    = "context"
	groupAutomation = "automation"
	groupSetup      = "setup"
)

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "docket",
		Short: "File-backed tasks and durable context for humans and agents",
		Long: `Docket keeps tasks, comments, and attachments as plain Markdown and YAML
files under .docket/, so a later human or agent can resume with full context.

  docket init                               create and register a workspace
  docket new --title "Fix login cache"      create a task and print its ID
  docket show TASK-0001                     read everything about a task
  docket comment TASK-0001 "Root cause..."  record a durable decision
  docket move TASK-0001 done                change its status

Pass TASK-ID explicitly in scripts and agents. Add --json to any command that
returns data; diagnostics always go to stderr.`,
		Example: `  docket list --status ready --label bug
  docket show TASK-0001 --view agent
  docket link TASK-0001 --blocks TASK-0002
  docket events --follow`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       Version,
	}
	root.PersistentFlags().BoolVar(&flagJSON, "json", false, "print machine-readable JSON from commands that return data")
	root.PersistentFlags().BoolVar(&flagCompact, "compact", false, "with --json, print single-line JSON")
	root.PersistentFlags().StringVar(&flagSession, "session", "", "session pointer used when TASK-ID is omitted (default $DOCKET_SESSION, then _global)")

	root.AddGroup(
		&cobra.Group{ID: groupTasks, Title: "Tasks:"},
		&cobra.Group{ID: groupContext, Title: "Relationships and context:"},
		&cobra.Group{ID: groupAutomation, Title: "Events and automation:"},
		&cobra.Group{ID: groupSetup, Title: "Setup and help:"},
	)
	root.SetHelpCommandGroupID(groupSetup)
	root.SetCompletionCommandGroupID(groupSetup)
	installHelp(root)

	workspaceCommand := newWorkspaceCmd()
	workspaceCommand.AddCommand(newWorkspaceReindexCmd())

	groups := map[string][]*cobra.Command{
		groupTasks: {
			newNewCmd(), newListCmd(), newShowCmd(), newEditCmd(), newMoveCmd(),
			newCommentCmd(), newLabelCmd(), newAttachFileCmd(), newWaitCmd(),
		},
		groupContext:    {newLinkCmd(), newReferenceCmd(), newProjectCmd(), newSessionCmd()},
		groupAutomation: {newEventsCmd(), newInboxCmd(), newRunCmd(), newServiceCmd(), newPluginCmd()},
		groupSetup:      {newInitCmd(), workspaceCommand, newDocsCmd(), newSkillCmd()},
	}
	for _, group := range []string{groupTasks, groupContext, groupAutomation, groupSetup} {
		setCommandGroup(group, groups[group]...)
		root.AddCommand(groups[group]...)
	}
	// Hidden: __lua-hook is the handler runner's Lua subprocess entry point.
	root.AddCommand(newLuaHookCmd())
	return root
}

func setCommandGroup(group string, commands ...*cobra.Command) {
	for _, command := range commands {
		command.GroupID = group
	}
}

// Execute runs the CLI and returns a process exit code.
func Execute() int {
	plugin.EngineVersion = Version
	handled, err := tryPluginPassthrough(os.Args[1:])
	if handled {
		if err != nil {
			fmt.Fprintf(os.Stderr, "docket: %v\n", err)
			return 1
		}
		return 0
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "docket: %v\n", err)
		return 1
	}
	return executeRoot(newRootCmd(), os.Stderr)
}

func executeRoot(root *cobra.Command, errorOutput io.Writer) int {
	if root.Context() == nil {
		root.SetContext(context.Background())
	}
	command, err := root.ExecuteC()
	if err == nil {
		return 0
	}
	fmt.Fprintf(errorOutput, "docket: %v\n", err)
	if !isUsageError(err) {
		return 1
	}
	if command == nil {
		command = root
	}
	fmt.Fprintf(errorOutput, "\nUsage: %s\n", command.UseLine())
	fmt.Fprintf(errorOutput, "Run '%s --help' for examples and flags.\n", command.CommandPath())
	return 1
}

func isUsageError(err error) bool {
	message := err.Error()
	for _, fragment := range []string{
		"accepts ",
		"requires at least ",
		"requires at most ",
		"requires exactly ",
		"unknown command ",
		"unknown flag:",
		"unknown shorthand flag:",
		"flag needs an argument:",
		"invalid argument ",
		"required flag(s)",
		"if any flags in the group ",
		"title is required",
		"title cannot be empty",
		"use either comment TEXT or --file",
		"no changes requested;",
		"no label changes requested;",
		"comment text is required",
		"specify a relationship flag",
		"specify exactly one relationship flag",
	} {
		if strings.Contains(message, fragment) {
			return true
		}
	}
	return false
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
//
// Task mutations use operationsFor instead, which records the event while the
// task lock is held and drains only after the lock is released.
func appendEvent(ws *workspace.Workspace, event events.Event) error {
	if err := recordEvent(ws, event); err != nil {
		return err
	}
	drainInline(ws)
	return nil
}

// recordEvent durably appends one event without running handlers.
func recordEvent(ws *workspace.Workspace, event events.Event) error {
	if err := events.Append(ws, event); err != nil {
		return fmt.Errorf("append event: %w", err)
	}
	return nil
}

// drainInline delivers pending events to inline handlers, reporting handler
// failures as warnings because they cannot roll back committed mutations.
func drainInline(ws *workspace.Workspace) {
	for _, failure := range handlers.DrainAll(ws, handlers.Options{Scope: handlers.ScopeInline, Output: os.Stderr, RefreshConfig: true}) {
		fmt.Fprintf(os.Stderr, "docket: warning: %s\n", failure.Error())
	}
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
	return "", fmt.Errorf("no task id given and no task attached to this session (use `docket session attach <id>` first)")
}

// printJSON writes v as JSON to stdout, indented unless --compact is set.
func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	if !flagCompact {
		enc.SetIndent("", "  ")
	}
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// jsonIndent marshals v as indented JSON bytes.
func jsonIndent(v any) ([]byte, error) {
	return json.MarshalIndent(v, "", "  ")
}
