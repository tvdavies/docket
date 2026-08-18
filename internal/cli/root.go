// Package cli wires docket's command surface (cobra) over the internal store
// packages. Every command supports --json for stable machine output; the
// default is concise human-readable text.
package cli

import (
	"encoding/json"
	"fmt"
	"io"
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

const (
	groupTasks      = "tasks"
	groupSessions   = "sessions"
	groupProjects   = "projects"
	groupAutomation = "automation"
	groupService    = "service"
	groupHelp       = "help"
)

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "docket",
		Short: "File-backed tasks and durable context for humans and agents",
		Long: `Docket stores tasks as plain Markdown and YAML files under .docket/.

The normal workflow uses explicit task IDs:
  1. docket init
  2. docket new --title "Describe the work"
  3. docket show TASK-0001
  4. docket comment TASK-0001 "Record a durable decision"
  5. docket move TASK-0001 done

Session attachment is optional shorthand for omitting TASK-ID. See
"docket session --help" before using it.`,
		Example: `  # Start a workspace and create a task
  docket init
  docket new --title "Fix login cache" --label bug

  # Inspect and update it using an explicit ID
  docket show TASK-0001
  docket comment TASK-0001 "Root cause: stale cache key"
  docket move TASK-0001 in-review`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       Version,
	}
	root.PersistentFlags().BoolVar(&flagJSON, "json", false, "request machine-readable JSON from data-returning commands")
	root.PersistentFlags().StringVar(&flagSession, "session", "", "session pointer used when TASK-ID is omitted (defaults to $DOCKET_SESSION, then _global)")

	root.AddGroup(
		&cobra.Group{ID: groupTasks, Title: "Task workflow:"},
		&cobra.Group{ID: groupSessions, Title: "Optional session shorthand:"},
		&cobra.Group{ID: groupProjects, Title: "Projects and relationships:"},
		&cobra.Group{ID: groupAutomation, Title: "Automation and event diagnostics:"},
		&cobra.Group{ID: groupService, Title: "Workspace, service, and maintenance:"},
		&cobra.Group{ID: groupHelp, Title: "Help and shell integration:"},
	)
	root.SetHelpCommandGroupID(groupHelp)
	root.SetCompletionCommandGroupID(groupHelp)

	taskCommands := []*cobra.Command{
		newInitCmd(), newNewCmd(), newListCmd(), newShowCmd(), newEditCmd(),
		newMoveCmd(), newCommentCmd(), newLabelCmd(), newAttachFileCmd(), newFilesCmd(),
	}
	setCommandGroup(groupTasks, taskCommands...)

	projectCommands := []*cobra.Command{newProjectCmd(), newLinkCmd(), newUnlinkCmd()}
	setCommandGroup(groupProjects, projectCommands...)

	automationCommands := []*cobra.Command{newInboxCmd(), newEventsCmd(), newWatchCmd()}
	setCommandGroup(groupAutomation, automationCommands...)

	serviceCommands := []*cobra.Command{newWorkspaceCmd(), newServeCmd(), newServiceCmd(), newReindexCmd()}
	setCommandGroup(groupService, serviceCommands...)

	sessionCommand := newSessionCmd()
	sessionCommand.GroupID = groupSessions
	skillCommand := newSkillCmd()
	skillCommand.GroupID = groupHelp

	// Preserve the original flat session commands and duplicate context command
	// for scripts, but keep the primary help surface small and unambiguous.
	legacyAttach, legacyDetach, legacyCurrent := newAttachCmd(), newDetachCmd(), newCurrentCmd()
	legacyContext := newContextCmd()
	for _, command := range []*cobra.Command{legacyAttach, legacyDetach, legacyCurrent, legacyContext} {
		command.Hidden = true
	}

	root.AddCommand(taskCommands...)
	root.AddCommand(projectCommands...)
	root.AddCommand(automationCommands...)
	root.AddCommand(serviceCommands...)
	root.AddCommand(sessionCommand, skillCommand)
	root.AddCommand(legacyAttach, legacyDetach, legacyCurrent, legacyContext, newLuaHookCmd())
	return root
}

func setCommandGroup(group string, commands ...*cobra.Command) {
	for _, command := range commands {
		command.GroupID = group
	}
}

// Execute runs the CLI and returns a process exit code.
func Execute() int {
	return executeRoot(newRootCmd(), os.Stderr)
}

func executeRoot(root *cobra.Command, errorOutput io.Writer) int {
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
