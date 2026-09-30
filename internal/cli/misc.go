package cli

import (
	_ "embed"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/tvdavies/docket/docs"
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

func newDocsCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "docs [topic]",
		Short:   "Print Docket's reference documentation, shipped with this binary",
		Long:    "Without a topic, lists the embedded documents. With one, prints it as Markdown. The docs match this binary's version, so prefer them over copies found elsewhere.",
		Example: "  docket docs\n  docket docs plugins/authoring\n  docket docs inbox",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				topics := docs.Topics()
				if flagJSON {
					return printJSON(topics)
				}
				width := 0
				for _, topic := range topics {
					width = max(width, len(topic.Name))
				}
				for _, topic := range topics {
					fmt.Printf("%-*s  %s\n", width, topic.Name, topic.Title)
				}
				fmt.Println("\nRead one with: docket docs <topic>")
				return nil
			}
			topic, content, ok := docs.Read(args[0])
			if !ok {
				return fmt.Errorf("unknown docs topic %q; run `docket docs` to list topics", args[0])
			}
			if flagJSON {
				return printJSON(map[string]string{"name": topic.Name, "title": topic.Title, "content": content})
			}
			fmt.Print(content)
			return nil
		},
	}
}
