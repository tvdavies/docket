package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/tvdavies/docket/internal/bundle"
)

func readStdin() ([]byte, error) {
	return io.ReadAll(os.Stdin)
}

// printBundleHuman renders a context bundle as readable markdown-ish text.
func printBundleHuman(b *bundle.Bundle) {
	fmt.Printf("# %s — %s\n", b.ID, b.Title)
	fmt.Printf("status: %s", b.Status)
	if b.Assignee != "" {
		fmt.Printf("   assignee: %s", b.Assignee)
	}
	if b.Project != nil {
		name := b.Project.Name
		if name == "" {
			name = b.Project.ID
		}
		fmt.Printf("   project: %s (%s)", name, b.Project.ID)
	}
	fmt.Println()
	if len(b.Labels) > 0 {
		fmt.Printf("labels: %s\n", strings.Join(b.Labels, ", "))
	}

	if b.Description != "" {
		fmt.Printf("\n%s\n", b.Description)
	}

	if len(b.Relationships) > 0 {
		fmt.Println("\n## Relationships")
		for kind, refs := range b.Relationships {
			var parts []string
			for _, r := range refs {
				if r.Title != "" {
					parts = append(parts, fmt.Sprintf("%s (%s)", r.ID, r.Title))
				} else {
					parts = append(parts, r.ID)
				}
			}
			fmt.Printf("- %s: %s\n", kind, strings.Join(parts, ", "))
		}
	}

	if len(b.Attachments) > 0 {
		fmt.Println("\n## Attachments")
		for _, a := range b.Attachments {
			line := fmt.Sprintf("- attachments/%s (%s)", a.File, a.Mime)
			if a.Caption != "" {
				line += " — " + a.Caption
			}
			fmt.Println(line)
		}
	}

	if len(b.Comments) > 0 {
		fmt.Printf("\n## Comments (%d)\n", len(b.Comments))
		for _, c := range b.Comments {
			fmt.Printf("\n[%s] %s\n%s\n", c.CreatedAt, c.Author, c.Body)
		}
	}
}
