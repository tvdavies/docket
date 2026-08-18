package cli

import (
	_ "embed"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/tvdavies/docket/internal/store"
	"github.com/tvdavies/docket/internal/task"
)

//go:embed skill.md
var skillDoc string

func newReindexCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "reindex",
		Short:   "Rebuild the optional derived task index",
		Long:    "Normal commands read authoritative task files directly. Run this only when a consumer explicitly needs .docket/.index/tasks.json.",
		Example: "  docket reindex\n  docket reindex --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWS()
			if err != nil {
				return err
			}
			tasks, err := task.All(ws)
			if err != nil {
				return err
			}
			data, err := jsonIndent(taskSummaries(tasks))
			if err != nil {
				return err
			}
			path := ws.Path(".index", "tasks.json")
			if err := store.WriteAtomic(path, data, 0o644); err != nil {
				return err
			}
			if flagJSON {
				return printJSON(map[string]any{"indexed": len(tasks), "path": path})
			}
			fmt.Printf("Indexed %d tasks → %s\n", len(tasks), path)
			return nil
		},
	}
}

func newSkillCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "skill",
		Aliases: []string{"guide"},
		Short:   "Print a self-contained usage guide for an agent harness",
		Example: "  docket skill\n  docket skill > ~/.config/my-agent/skills/docket.md",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if flagJSON {
				return printJSON(map[string]string{"skill": skillDoc})
			}
			fmt.Print(skillDoc)
			return nil
		},
	}
}
