package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tvdavies/docket/internal/registry"
	"github.com/tvdavies/docket/internal/store"
)

const unitName = "docket.service"

// SystemdUnitPath returns the per-user unit path.
func SystemdUnitPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "systemd", "user", unitName), nil
}

// InstallSystemdUnit writes the user unit and asks systemd to reload it. It
// deliberately does not start the service or enable login lingering.
func InstallSystemdUnit() (string, error) {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return "", fmt.Errorf("systemctl is not available; Docket user services require systemd")
	}
	binary, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(binary); err == nil {
		binary = resolved
	}
	configPath, err := registry.ConfigPath()
	if err != nil {
		return "", err
	}
	unitPath, err := SystemdUnitPath()
	if err != nil {
		return "", err
	}
	unit := BuildSystemdUnit(binary, configPath, os.Getenv("PATH"))
	if err := store.WriteAtomic(unitPath, []byte(unit), 0o644); err != nil {
		return "", err
	}
	if err := RunSystemctl("daemon-reload"); err != nil {
		return "", err
	}
	return unitPath, nil
}

// UninstallSystemdUnit stops, disables, and removes the user unit. Workspace
// registrations and task files are untouched.
func UninstallSystemdUnit() error {
	unitPath, err := SystemdUnitPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(unitPath); err == nil {
		if err := RunSystemctl("disable", "--now", unitName); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Remove(unitPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return RunSystemctl("daemon-reload")
}

// RunSystemctl executes a user-scoped systemctl command with attached output.
func RunSystemctl(args ...string) error {
	commandArgs := append([]string{"--user"}, args...)
	cmd := exec.Command("systemctl", commandArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("systemctl %s: %w", strings.Join(commandArgs, " "), err)
	}
	return nil
}

// RunJournal follows the Docket user-service journal.
func RunJournal() error {
	cmd := exec.Command("journalctl", "--user", "-u", unitName, "-f")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("journalctl: %w", err)
	}
	return nil
}

// BuildSystemdUnit is separated for exact unit tests.
func BuildSystemdUnit(binary, configPath, pathEnv string) string {
	return `[Unit]
Description=Docket multi-workspace task service
After=network.target

[Service]
Type=simple
ExecStart=` + systemdQuote(binary) + ` serve --all
Environment=` + systemdQuote("DOCKET_CONFIG="+configPath) + `
Environment=` + systemdQuote("PATH="+pathEnv) + `
EnvironmentFile=-%h/.config/docket/environment
Restart=on-failure
RestartSec=5s
KillMode=mixed
TimeoutStopSec=15s

[Install]
WantedBy=default.target
`
}

func systemdQuote(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	value = strings.ReplaceAll(value, `%`, `%%`)
	return `"` + value + `"`
}
