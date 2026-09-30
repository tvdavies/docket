package cli

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/tvdavies/docket/internal/registry"
	docketservice "github.com/tvdavies/docket/internal/service"
)

func newRunCmd() *cobra.Command {
	var all, once bool
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run the headless event runner: watch workspaces and deliver hooks",
		Long: `run watches workspaces and drains every configured handler, including
"delivery: service" hooks that mutating commands leave for the runner. It
opens no network listener.

By default it runs in the foreground for the current workspace until
interrupted. With --all it follows the machine-local workspace registry,
starting and stopping workspace watchers as registrations change, and
reloads plugin manifests and config.yaml hook changes as they happen.

--once performs one bounded drain of pending events and exits, for a
heartbeat, cron job or scheduler. Events that fail or cannot be processed stay
pending for the next drain; the originating task changes are never rolled
back. The exit status is non-zero if any handler failed. With --all, a
registered workspace whose directory no longer exists is reported as
"missing" and does not fail the run; remove it with "docket workspace
remove", or let a long-running "docket run --all" prune it.

"docket service" installs this as a systemd user unit running "run --all".`,
		Example: `  docket run                # current workspace, foreground
  docket run --all          # all registered workspaces, foreground
  docket run --once --all   # deliver pending hook events once and exit`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runEventRunner(cmd, all, once)
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "run every workspace in the machine-local registry")
	cmd.Flags().BoolVar(&once, "once", false, "drain pending events once and exit")
	return cmd
}

// newServeCmd keeps "serve" working for existing unit files and scripts for
// one transition. It runs the same headless runner; the board, API and
// listen flags are gone.
func newServeCmd() *cobra.Command {
	var all bool
	var listen string
	var allowRemote bool
	cmd := &cobra.Command{
		Use:        "serve",
		Short:      "Deprecated alias for \"docket run\" (no web board or HTTP API)",
		Deprecated: "use \"docket run\"; Docket no longer serves a web board or HTTP API",
		Args:       cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if listen != "" || allowRemote {
				return fmt.Errorf("--listen and --allow-remote are no longer supported: Docket no longer serves a web board or HTTP API; run \"docket run%s\" for hook delivery", map[bool]string{true: " --all"}[all])
			}
			return runEventRunner(cmd, all, false)
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "run every workspace in the machine-local registry")
	cmd.Flags().StringVar(&listen, "listen", "", "removed; rejected with an explanation")
	cmd.Flags().BoolVar(&allowRemote, "allow-remote", false, "removed; rejected with an explanation")
	_ = cmd.Flags().MarkHidden("listen")
	_ = cmd.Flags().MarkHidden("allow-remote")
	return cmd
}

func runEventRunner(cmd *cobra.Command, all, once bool) error {
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var entries []registry.WorkspaceEntry
	var config *registry.Config
	if all {
		loaded, err := registry.Load()
		if err != nil {
			return err
		}
		config = loaded
		entries = config.Workspaces
	} else {
		ws, err := openWS()
		if err != nil {
			return err
		}
		root := filepath.Dir(ws.Root)
		entries = []registry.WorkspaceEntry{{Name: filepath.Base(root), Path: root}}
	}

	if once {
		if config == nil {
			loaded, err := registry.Load()
			if err != nil {
				return err
			}
			config = loaded
		}
		results, err := docketservice.RunOnce(ctx, entries, os.Stderr, docketservice.OnceOptions{SkipMissing: all, Registry: config})
		if flagJSON {
			if printErr := printJSON(results); printErr != nil {
				return printErr
			}
		} else {
			for _, result := range results {
				fmt.Printf("%s\t%s\t%d events\t%d handlers\n", result.Name, result.State, result.EventCount, result.HandlerCount)
			}
		}
		if err != nil {
			return fmt.Errorf("drain incomplete; failed events stay pending for the next run: %w", err)
		}
		return nil
	}

	manager := docketservice.NewManager(ctx, os.Stderr)
	defer manager.Stop()
	if all {
		go manager.FollowRegistry(ctx, 2*time.Second)
	} else {
		manager.SetWorkspaces(entries)
		go manager.WatchPlugins(ctx, 2*time.Second)
	}
	fmt.Fprintf(os.Stderr, "docket: event runner started for %d workspace(s); press Ctrl-C to stop\n", len(entries))
	<-ctx.Done()
	return nil
}

func newServiceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "service",
		Short: "Install and control the optional systemd user unit for the event runner",
		Long: `There is one Docket user service per machine, not one per workspace. It
runs ` + "`docket run --all`" + ` in the background so "delivery: service" hooks
are delivered without a foreground process. It is optional: containers and
other supervisors can run ` + "`docket run --all`" + ` directly, and schedulers can
call ` + "`docket run --once --all`" + `.

"install" rewrites the unit, so re-run it after upgrading from a release whose
unit ran "serve --all".`,
		Example: `  docket service install
  docket service start
  docket service status
  docket service logs`,
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "install",
			Short: "Install the systemd user unit without starting it",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				path, err := docketservice.InstallSystemdUnit()
				if err != nil {
					return err
				}
				fmt.Printf("Installed %s\n", path)
				fmt.Println("Run `docket service start` to enable and start it.")
				fmt.Println("To keep it running outside login sessions, explicitly run: loginctl enable-linger \"$USER\"")
				return nil
			},
		},
		&cobra.Command{
			Use:   "start",
			Short: "Enable and start the Docket user service",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				return docketservice.RunSystemctl("enable", "--now", "docket.service")
			},
		},
		&cobra.Command{
			Use:   "stop",
			Short: "Stop the Docket user service (leave it enabled)",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				return docketservice.RunSystemctl("stop", "docket.service")
			},
		},
		&cobra.Command{
			Use:   "restart",
			Short: "Restart the Docket user service",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				return docketservice.RunSystemctl("restart", "docket.service")
			},
		},
		&cobra.Command{
			Use:   "status",
			Short: "Show systemd status for the Docket user service",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				return docketservice.RunSystemctl("status", "--no-pager", "docket.service")
			},
		},
		&cobra.Command{
			Use:   "logs",
			Short: "Follow the Docket user-service journal",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				return docketservice.RunJournal()
			},
		},
		&cobra.Command{
			Use:   "uninstall",
			Short: "Stop, disable, and remove the systemd user unit",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				if err := docketservice.UninstallSystemdUnit(); err != nil {
					return err
				}
				fmt.Println("Uninstalled docket.service (workspaces and task files untouched).")
				return nil
			},
		},
	)
	return cmd
}
