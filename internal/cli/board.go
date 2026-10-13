package cli

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tvdavies/docket/internal/actions"
	"github.com/tvdavies/docket/internal/bundle"
	"github.com/tvdavies/docket/internal/events"
	"github.com/tvdavies/docket/internal/handlers"
	"github.com/tvdavies/docket/internal/task"
	"github.com/tvdavies/docket/internal/tui"
	"github.com/tvdavies/docket/internal/workspace"
)

// errNoTerminal reports that the board needs an interactive terminal.
var errNoTerminal = errors.New("the board needs an interactive terminal; use `docket list` or `docket show TASK-ID` in scripts")

func newBoardCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "board",
		Aliases: []string{"tui"},
		Short:   "Open the interactive task board",
		Long: `Board opens a full-screen board of the workspace's status lanes. Running
docket with no arguments in a terminal opens it too.

Navigate lanes with h/l (or arrows, tab), cards with j/k, filter with /, and
press enter to read a task: its description, comments, activity timeline, and
links. m moves the selected task, c adds a comment, r reloads, ? shows every
key, and q or esc goes back or quits. The board refreshes when the workspace
changes on disk.`,
		Example: `  docket board
  docket tui`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !stdinIsTerminal() || !stdoutIsTerminal() {
				return errNoTerminal
			}
			return runBoard(cmd)
		},
	}
}

// runRoot handles bare `docket`: the board for a person at a terminal, the
// help text everywhere else (pipes, agents, CI, --json).
func runRoot(cmd *cobra.Command, args []string) error {
	if len(args) > 0 || flagJSON || !stdinIsTerminal() || !stdoutIsTerminal() {
		return cmd.Help()
	}
	return runBoard(cmd)
}

// openBoardWorkspace resolves the workspace the board shows. It is the single
// place to change when workspace selection changes.
func openBoardWorkspace() (*workspace.Workspace, error) {
	return openWS()
}

func runBoard(cmd *cobra.Command) error {
	ws, err := openBoardWorkspace()
	if err != nil {
		if errors.Is(err, workspace.ErrNotFound) {
			return fmt.Errorf("%w\n\nThe board shows a docket workspace. Create one here with `docket init`, or run\ndocket inside a project that has a .docket directory. `docket --help` lists every command", err)
		}
		return err
	}
	source := &boardSource{root: ws.Root, name: filepath.Base(filepath.Dir(ws.Root))}
	return tui.Run(cmd.Context(), source, tui.Options{WatchDir: ws.Root})
}

// boardSource backs the board with the same packages the CLI commands use.
// The workspace is reopened on each load so config changes (new lanes) show
// up on refresh.
type boardSource struct {
	root string
	name string
}

func (s *boardSource) open() (*workspace.Workspace, error) {
	return workspace.OpenRoot(s.root)
}

func (s *boardSource) Load() (tui.Snapshot, error) {
	ws, err := s.open()
	if err != nil {
		return tui.Snapshot{}, err
	}
	tasks, err := task.All(ws)
	if err != nil {
		return tui.Snapshot{}, err
	}
	return tui.Snapshot{Name: s.name, Statuses: ws.Config.Statuses, Tasks: tasks}, nil
}

func (s *boardSource) Detail(id string) (*bundle.Bundle, error) {
	ws, err := s.open()
	if err != nil {
		return nil, err
	}
	return bundle.BuildWith(ws, id, bundle.Options{})
}

func (s *boardSource) Move(id, status string) (string, error) {
	ws, err := s.open()
	if err != nil {
		return "", err
	}
	operations, warnings := boardOperations(ws)
	_, _, err = operations.Move(id, status)
	return warnings(), err
}

func (s *boardSource) Comment(id, text string) (string, error) {
	ws, err := s.open()
	if err != nil {
		return "", err
	}
	operations, warnings := boardOperations(ws)
	_, err = operations.Comment(id, text)
	return warnings(), err
}

// boardOperations is operationsFor with handler output captured instead of
// written to stderr, which would corrupt the full-screen board. Events and
// inline handler delivery are otherwise identical to the CLI commands.
func boardOperations(ws *workspace.Workspace) (actions.Tasks, func() string) {
	var output bytes.Buffer
	var failures []string
	operations := actions.Tasks{
		Workspace: ws, Actor: actor(), Session: sessionID(),
		Append: func(event events.Event) error { return recordEvent(ws, event) },
		Committed: func() {
			for _, failure := range handlers.DrainAll(ws, handlers.Options{Scope: handlers.ScopeInline, Output: &output, RefreshConfig: true}) {
				failures = append(failures, "warning: "+failure.Error())
			}
		},
	}
	return operations, func() string { return strings.Join(failures, "; ") }
}
