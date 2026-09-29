package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/cobra"
	"github.com/tvdavies/docket/internal/plugin"
	"github.com/tvdavies/docket/internal/plugin/scaffold"
	"github.com/tvdavies/docket/internal/pluginmgr"
	"github.com/tvdavies/docket/internal/registry"
	docketservice "github.com/tvdavies/docket/internal/service"
	"github.com/tvdavies/docket/internal/workspace"
)

func newPluginNewCmd() *cobra.Command {
	var options scaffold.Options
	var dir string
	command := &cobra.Command{
		Use:   "new NAME",
		Short: "Scaffold a new plugin directory from built-in templates",
		Long: `Scaffold a plugin that validates and runs as written. Without view flags it
gets a task widget, a task panel and a workspace page. --service adds a
dependency-free Node service that Docket supervises, plus a setting whose
choices come from it (options_from). Continue with: docket plugin dev DIR`,
		Example: "  docket plugin new my-plugin\n  docket plugin new build-status --widget --service --dir ~/dev/build-status",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			options.Name = args[0]
			options.MinVersion = Version
			if builtinCommand(options.Name) {
				return fmt.Errorf("plugin name %q collides with a builtin docket command", options.Name)
			}
			if dir == "" {
				dir = options.Name
			}
			written, err := scaffold.Create(dir, options)
			if err != nil {
				return err
			}
			if flagJSON {
				return printJSON(map[string]any{"name": options.Name, "path": dir, "files": written})
			}
			fmt.Printf("Created plugin %s in %s:\n", options.Name, dir)
			for _, file := range written {
				fmt.Printf("  %s\n", file)
			}
			fmt.Printf("\nNext: docket plugin dev %s\n", dir)
			return nil
		},
	}
	command.Flags().StringVar(&dir, "dir", "", "directory to create (default ./NAME)")
	command.Flags().StringVar(&options.Description, "description", "", "manifest description")
	command.Flags().BoolVar(&options.Widget, "widget", false, "include a task widget and a CLI that publishes it")
	command.Flags().BoolVar(&options.Panel, "panel", false, "include a task detail panel")
	command.Flags().BoolVar(&options.Page, "page", false, "include a workspace page")
	command.Flags().BoolVar(&options.Service, "service", false, "include a supervised Node service")
	command.Flags().IntVar(&options.Port, "port", 0, "service port (default derived from the name)")
	return command
}

// validation is the result of checking a plugin directory.
type validation struct {
	Path     string   `json:"path"`
	Name     string   `json:"name,omitempty"`
	Version  string   `json:"version,omitempty"`
	Valid    bool     `json:"valid"`
	Error    string   `json:"error,omitempty"`
	Problems []string `json:"problems"`
}

func validatePlugin(path string) validation {
	result := validation{Path: path, Problems: []string{}}
	manifest, err := plugin.Load(path, Version)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Name, result.Version = manifest.Name, manifest.Version
	result.Problems = append(result.Problems, manifest.Problems()...)
	if builtinCommand(manifest.Name) {
		result.Problems = append(result.Problems, fmt.Sprintf("name %q collides with a builtin docket command", manifest.Name))
	}
	result.Valid = len(result.Problems) == 0
	return result
}

func (v validation) String() string {
	switch {
	case v.Error != "":
		return fmt.Sprintf("invalid manifest: %s", v.Error)
	case !v.Valid:
		return fmt.Sprintf("%s %s: %d problem(s):\n  %s", v.Name, v.Version, len(v.Problems), strings.Join(v.Problems, "\n  "))
	default:
		return fmt.Sprintf("%s %s: ok", v.Name, v.Version)
	}
}

func newPluginValidateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate [PATH]",
		Short: "Check a plugin directory's manifest and the files it references",
		Long: `Validate the manifest exactly as install and enable do, then check that the
files it references exist: handler and CLI scripts (and that they are
executable), service.command, ui.dir and every UI entry. Exits non-zero on any
problem. Workspace-specific checks (required config, status anchors) happen
at enable time.`,
		Example: "  docket plugin validate\n  docket plugin validate ~/dev/my-plugin --json",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := "."
			if len(args) == 1 {
				path = args[0]
			}
			result := validatePlugin(path)
			if flagJSON {
				if err := printJSON(result); err != nil {
					return err
				}
			} else if result.Valid {
				fmt.Fprintln(cmd.OutOrStdout(), result)
			}
			if !result.Valid {
				return errors.New(result.String())
			}
			return nil
		},
	}
}

func newPluginDevCmd() *cobra.Command {
	var workspacePath string
	var settings []string
	var serve bool
	command := &cobra.Command{
		Use:   "dev [PATH]",
		Short: "Link, enable and watch a plugin under development",
		Long: `dev is the edit loop for a plugin checkout. It:

  1. validates the plugin and links it (docket plugin add PATH);
  2. enables it in the workspace, unless it already is (--set supplies config);
  3. starts an in-process Docket service if none is listening (see --serve);
  4. re-validates the manifest on every save, and streams the service's reload
     events and the plugin service log until interrupted.

Edits under ui.dir reload open frames in place; manifest edits reload the
plugin; files matched by service.watch restart its service. The plugin stays
installed and enabled on exit.`,
		Example: "  docket plugin dev .\n  docket plugin dev ~/dev/my-plugin --set token=abc",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := "."
			if len(args) == 1 {
				path = args[0]
			}
			root, err := filepath.Abs(path)
			if err != nil {
				return err
			}
			values, err := parseSettings(settings)
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			out := cmd.OutOrStdout()
			return runPluginDev(ctx, out, root, workspacePath, values, serve)
		},
	}
	command.Flags().StringVar(&workspacePath, "workspace", ".", "workspace to enable the plugin in")
	command.Flags().StringArrayVar(&settings, "set", nil, "workspace config key=value when enabling (repeatable)")
	command.Flags().BoolVar(&serve, "serve", true, "serve the workspace in-process when no Docket service is listening")
	return command
}

func runPluginDev(ctx context.Context, out io.Writer, root, workspacePath string, values map[string]any, serve bool) error {
	result := validatePlugin(root)
	if result.Error != "" {
		return errors.New(result.String())
	}
	fmt.Fprintln(out, result)
	entry, err := pluginmgr.Add(root, "", Version)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Linked %s → %s\n", entry.Name, entry.Path)
	enabled, err := workspace.DeclaresPluginRoot(workspacePath, entry.Name)
	if err != nil {
		return err
	}
	if !enabled {
		if err := pluginmgr.Enable(workspacePath, entry.Name, values, false, false, Version); err != nil {
			return fmt.Errorf("enable %s: %w (fix it, then rerun docket plugin dev)", entry.Name, err)
		}
		fmt.Fprintf(out, "Enabled %s in %s\n", entry.Name, workspacePath)
	} else if len(values) > 0 {
		return fmt.Errorf("%s is already enabled; change settings with docket plugin enable %s --set or in the web UI", entry.Name, entry.Name)
	}

	config, err := registry.Load()
	if err != nil {
		return err
	}
	base := "http://" + config.Listen
	served := make(chan error, 1)
	events := make(chan string, 64)
	if !listening(base) {
		if !serve {
			fmt.Fprintf(out, "No Docket service at %s; start one with docket serve or docket service start.\n", base)
		} else {
			wsRoot, err := workspace.FindRootAt(workspacePath)
			if err != nil {
				return err
			}
			project := filepath.Dir(wsRoot)
			manager := docketservice.NewManager(ctx, &prefixWriter{prefix: "[docket] ", out: events})
			defer manager.Stop()
			manager.SetWorkspaces([]registry.WorkspaceEntry{{Name: filepath.Base(project), Path: project}})
			go manager.WatchPlugins(ctx, 2*time.Second)
			go func() { served <- docketservice.Serve(ctx, config.Listen, manager, io.Discard) }()
			fmt.Fprintf(out, "Serving %s at %s (in-process; stops with this command)\n", filepath.Base(project), base)
		}
	} else {
		fmt.Fprintf(out, "Using the Docket service at %s\n", base)
	}

	go watchManifest(ctx, root, events)
	go followPluginStream(ctx, base, entry.Name, events)
	if logPath, err := docketservice.PluginLogPath(entry.Name); err == nil {
		var offset int64
		if info, err := os.Stat(logPath); err == nil {
			offset = info.Size()
		}
		go func() { _ = followLog(ctx.Done(), logPath, offset, &prefixWriter{prefix: "[service] ", out: events}) }()
	}
	fmt.Fprintln(out, "Watching for changes; Ctrl-C to stop. The plugin stays installed and enabled.")
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-served:
			if err != nil {
				return fmt.Errorf("in-process service: %w", err)
			}
			return nil
		case line := <-events:
			fmt.Fprintf(out, "%s %s\n", time.Now().Format("15:04:05"), line)
		}
	}
}

func listening(base string) bool {
	client := http.Client{Timeout: time.Second}
	response, err := client.Get(base + "/healthz")
	if err != nil {
		return false
	}
	response.Body.Close()
	return response.StatusCode == http.StatusOK
}

// watchManifest re-validates the plugin whenever its manifest is written.
// Editors often replace files by rename, so the directory is watched.
func watchManifest(ctx context.Context, root string, events chan<- string) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		events <- "cannot watch the manifest: " + err.Error()
		return
	}
	defer watcher.Close()
	if err := watcher.Add(root); err != nil {
		events <- "cannot watch the manifest: " + err.Error()
		return
	}
	var debounce <-chan time.Time
	last := ""
	for {
		select {
		case <-ctx.Done():
			return
		case event, open := <-watcher.Events:
			if !open {
				return
			}
			if filepath.Base(event.Name) == plugin.ManifestFile {
				debounce = time.After(150 * time.Millisecond)
			}
		case <-watcher.Errors:
		case <-debounce:
			if report := "manifest " + validatePlugin(root).String(); report != last {
				last = report
				events <- report
			}
		}
	}
}

// followPluginStream reports changes to one plugin from GET /api/stream,
// reconnecting while the service restarts.
func followPluginStream(ctx context.Context, base, name string, events chan<- string) {
	var previous *docketservice.PluginState
	connected := false
	for ctx.Err() == nil {
		err := readPluginStream(ctx, base, func(states []docketservice.PluginState) {
			if !connected {
				connected = true
				events <- "connected to " + base + "/api/stream"
			}
			var current *docketservice.PluginState
			for index := range states {
				if states[index].Name == name {
					current = &states[index]
				}
			}
			for _, line := range describePluginChange(previous, current) {
				events <- line
			}
			previous = current
		})
		if ctx.Err() != nil {
			return
		}
		if connected {
			connected = false
			events <- fmt.Sprintf("lost %s/api/stream (%v); reconnecting", base, err)
		}
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
		}
	}
}

func readPluginStream(ctx context.Context, base string, handle func([]docketservice.PluginState)) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/stream", nil)
	if err != nil {
		return err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("status %s", response.Status)
	}
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 64*1024), 8<<20)
	event := ""
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:") && event == "plugins":
			var payload struct {
				Plugins []docketservice.PluginState `json:"plugins"`
			}
			if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &payload); err == nil {
				handle(payload.Plugins)
			}
		case line == "":
			event = ""
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return io.EOF
}

// describePluginChange turns two successive snapshots of one plugin into
// human-readable reload events.
func describePluginChange(previous, current *docketservice.PluginState) []string {
	if current == nil {
		if previous != nil {
			return []string{"plugin is no longer installed"}
		}
		return nil
	}
	var lines []string
	if previous == nil {
		line := fmt.Sprintf("plugin %s %s loaded", current.Name, current.Version)
		if current.UIBase != "" {
			line += " (ui " + current.UIBase + ")"
		}
		lines = append(lines, line)
		if current.Error != "" {
			lines = append(lines, "plugin error: "+current.Error)
		}
	} else {
		if current.Error != previous.Error {
			if current.Error != "" {
				lines = append(lines, "plugin error: "+current.Error)
			} else {
				lines = append(lines, "plugin error cleared")
			}
		}
		if current.ManifestHash != previous.ManifestHash {
			lines = append(lines, fmt.Sprintf("manifest reloaded (%s)", current.Version))
		}
		if current.UIHash != previous.UIHash && current.UIHash != "" {
			lines = append(lines, "ui reloaded: open frames now use "+current.UIBase)
		}
	}
	var before *docketservice.ServiceStatus
	if previous != nil {
		before = previous.Service
	}
	after := current.Service
	switch {
	case after == nil && before != nil:
		lines = append(lines, "service stopped")
	case after != nil && (before == nil || after.State != before.State || after.Restarts != before.Restarts):
		line := "service " + after.State
		if after.PID != 0 {
			line += fmt.Sprintf(" (pid %d)", after.PID)
		}
		if after.Restarts > 0 {
			line += fmt.Sprintf(", %d restart(s)", after.Restarts)
		}
		if after.LastError != "" && after.State != "running" && after.State != "healthy" {
			line += ": " + after.LastError
		}
		lines = append(lines, line)
	}
	return lines
}

// prefixWriter forwards complete lines to a channel with a prefix. It is safe
// for concurrent writers.
type prefixWriter struct {
	prefix  string
	out     chan<- string
	mu      sync.Mutex
	pending []byte
}

func (w *prefixWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pending = append(w.pending, data...)
	for {
		index := strings.IndexByte(string(w.pending), '\n')
		if index < 0 {
			return len(data), nil
		}
		w.out <- w.prefix + string(w.pending[:index])
		w.pending = w.pending[index+1:]
	}
}
