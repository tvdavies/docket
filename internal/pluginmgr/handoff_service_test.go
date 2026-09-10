package pluginmgr_test

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tvdavies/docket/internal/pluginmgr"
	"github.com/tvdavies/docket/internal/registry"
	"github.com/tvdavies/docket/internal/service"
)

// The subprocess runs the real Manager/config watcher, not a simulated drain.
// Its long-lived child represents an already accepted detached supervisor. No
// service unit, fleet hook, agent harness or live executable is invoked.
func TestHandoffServiceAndSupervisorContinuity(t *testing.T) {
	f := newFixture(t)
	f.appendMove("done")
	if failures := f.drain(); len(failures) != 0 {
		t.Fatal(failures)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestHandoffServiceHelper$")
	cmd.Dir = f.home
	cmd.Env = append(os.Environ(), "DOCKET_TEST_SERVICE="+f.project)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		stdin.Close()
		if err := cmd.Wait(); err != nil {
			t.Errorf("fixture service: %v", err)
		}
	}()
	lines := bufio.NewScanner(stdout)
	if !lines.Scan() || !strings.HasPrefix(lines.Text(), "READY ") {
		t.Fatalf("service ready: %s", lines.Text())
	}
	original := strings.TrimPrefix(lines.Text(), "READY ")

	for index, direction := range []pluginmgr.Direction{pluginmgr.Forward, pluginmgr.Reverse, pluginmgr.Forward} {
		entered, release := make(chan struct{}), make(chan struct{})
		pluginmgr.SetCrashHook(func(stage string) {
			if stage == "before_publish" {
				close(entered)
				<-release
			}
		})
		finished := make(chan error, 1)
		go func() {
			var err error
			if direction == pluginmgr.Forward {
				_, err = f.forward(f.receiptDir(fmt.Sprintf("service-%d", index)))
			} else {
				_, err = f.reverse(f.receiptDir(fmt.Sprintf("service-%d", index)))
			}
			finished <- err
		}()
		select {
		case <-entered:
		case err := <-finished:
			t.Fatalf("handoff before barrier: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		// The service is blocked by the held locks. This event is pending before
		// publication; no append after publication may be needed to wake delivery.
		f.appendMove("done")
		close(release)
		if err := <-finished; err != nil {
			t.Fatal(err)
		}
		pluginmgr.SetCrashHook(nil)
		target := index + 2
		deadline := time.After(5 * time.Second)
		ticker := time.NewTicker(5 * time.Millisecond)
		for {
			all := true
			for _, name := range f.names {
				if len(f.deliveries(name)) != target {
					all = false
				}
			}
			if all {
				break
			}
			select {
			case <-deadline:
				t.Fatal("config watcher did not resume pending delivery without a new event")
			case <-ticker.C:
			}
		}
		ticker.Stop()
		for _, name := range f.names {
			f.assertNoDuplicates(name)
		}
		fmt.Fprintln(stdin, "probe")
		if !lines.Scan() || lines.Text() != "PROBE "+original {
			t.Fatalf("service/supervisor identity changed: %s, originally %s", lines.Text(), original)
		}
	}
	fmt.Fprintln(stdin, "stop")
}

func TestHandoffServiceHelper(t *testing.T) {
	project := os.Getenv("DOCKET_TEST_SERVICE")
	if project == "" {
		return
	}
	home := os.Getenv("HOME")
	if !strings.HasPrefix(project, home+string(filepath.Separator)) || os.Getenv("DOCKET_CONFIG") != filepath.Join(home, "registry.yaml") || os.Getenv("DISPATCH_META") != "" {
		t.Fatal("unsafe fixture environment")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager := service.NewManager(ctx, os.Stderr)
	defer manager.Stop()
	manager.SetWorkspaces([]registry.WorkspaceEntry{{Name: "fixture", Path: project}})
	deadline := time.After(5 * time.Second)
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		statuses := manager.Statuses()
		if len(statuses) == 1 && statuses[0].State == "watching" {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("manager not watching: %+v", statuses)
		case <-ticker.C:
		}
	}
	supervisor := exec.Command(os.Args[0], "-test.run=^TestHandoffSupervisorHelper$")
	supervisor.Env = append(os.Environ(), "DOCKET_TEST_SUPERVISOR=1")
	input, err := supervisor.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := supervisor.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := supervisor.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		input.Close()
		if err := supervisor.Wait(); err != nil {
			t.Error(err)
		}
	}()
	scanner := bufio.NewScanner(output)
	if !scanner.Scan() || scanner.Text() != "READY" {
		t.Fatal("supervisor not ready")
	}
	identity := fmt.Sprintf("%d %d", os.Getpid(), supervisor.Process.Pid)
	fmt.Println("READY " + identity)
	commands := bufio.NewScanner(os.Stdin)
	for commands.Scan() {
		if commands.Text() == "stop" {
			return
		}
		fmt.Fprintln(input, "probe")
		if !scanner.Scan() || scanner.Text() != "ALIVE" {
			t.Fatal("supervisor failed continuity probe")
		}
		fmt.Println("PROBE " + identity)
	}
}

func TestHandoffSupervisorHelper(t *testing.T) {
	if os.Getenv("DOCKET_TEST_SUPERVISOR") != "1" {
		return
	}
	fmt.Println("READY")
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		fmt.Println("ALIVE")
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		t.Fatal(err)
	}
}
