package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tvdavies/docket/internal/plugin"
)

// validation is the result of checking a plugin directory.
type validation struct {
	Path     string   `json:"path"`
	Name     string   `json:"name,omitempty"`
	Version  string   `json:"version,omitempty"`
	Valid    bool     `json:"valid"`
	Error    string   `json:"error,omitempty"`
	Problems []string `json:"problems"`
	// Warnings describe legacy metadata that no longer has an effect; they do
	// not make a plugin invalid.
	Warnings []string `json:"warnings"`
}

func validatePlugin(path string) validation {
	result := validation{Path: path, Problems: []string{}, Warnings: []string{}}
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
	if problem := manifest.HostingProblem(); problem != "" {
		result.Warnings = append(result.Warnings, problem)
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
files it references exist: handler and CLI scripts, and that executables are
executable. Exits non-zero on any problem. Workspace-specific checks (required
config, status anchors) happen at enable time.

Legacy ui, service and options_from metadata is still parsed and validated,
but its files are not required. A service.command is reported as a warning:
Docket no longer launches plugin processes.`,
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
			} else {
				for _, warning := range result.Warnings {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", warning)
				}
				if result.Valid {
					fmt.Fprintln(cmd.OutOrStdout(), result)
				}
			}
			if !result.Valid {
				return errors.New(result.String())
			}
			return nil
		},
	}
}
