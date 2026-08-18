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

func newServeCmd() *cobra.Command {
	var all, allowRemote bool
	var listen string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Watch handlers and serve the local Docket Kanban board",
		Long: `serve runs one foreground Docket service. By default it serves the current
workspace. With --all it follows the machine-local workspace registry and
starts or stops workspace runtimes as registrations change. This is the
foreground/debugging form; use "docket service" to manage the background systemd
user service.`,
		Example: `  docket serve            # current workspace, foreground
  docket serve --all      # all registered workspaces, foreground
  docket serve --all --listen 127.0.0.1:7463`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			config, err := registry.Load()
			if err != nil {
				return err
			}
			if listen == "" {
				listen = config.Listen
			}
			if err := docketservice.ValidateListen(listen, allowRemote); err != nil {
				return err
			}
			manager := docketservice.NewManager(ctx, os.Stderr)
			defer manager.Stop()
			if all {
				go manager.FollowRegistry(ctx, 2*time.Second)
			} else {
				ws, err := openWS()
				if err != nil {
					return err
				}
				root := filepath.Dir(ws.Root)
				manager.SetWorkspaces([]registry.WorkspaceEntry{{Name: filepath.Base(root), Path: root}})
			}
			return docketservice.Serve(ctx, listen, manager, os.Stderr)
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "serve every workspace in the machine-local registry")
	cmd.Flags().BoolVar(&allowRemote, "allow-remote", false, "allow an unauthenticated non-loopback HTTP bind")
	cmd.Flags().StringVar(&listen, "listen", "", "HTTP listen address (defaults to service config, 127.0.0.1:7463)")
	return cmd
}

func newServiceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "service",
		Short: "Install and control the background systemd user service",
		Long: `There is one Docket user service per machine, not one per workspace. It
runs ` + "`docket serve --all`" + ` in the background. Use ` + "`docket serve`" + ` directly only
for foreground development or debugging.`,
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
