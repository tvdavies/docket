package cli

import (
	"encoding/json"
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
	if b.Wait != nil {
		fmt.Printf("waiting: %s — %s (%s)\n", b.Wait.Kind, b.Wait.Reason, b.Wait.ID)
		if b.Wait.Reference != "" {
			fmt.Printf("wait reference: %s\n", b.Wait.Reference)
		}
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

	if len(b.References) > 0 {
		fmt.Println("\n## References")
		for _, reference := range b.References {
			line := fmt.Sprintf("- %s [%s]: %s", reference.ID, reference.Kind, reference.URL)
			if reference.Title != "" {
				line += " — " + reference.Title
			}
			fmt.Println(line)
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

	if len(b.Activity) > 0 {
		fmt.Printf("\n## Activity (%d)\n", len(b.Activity))
		for _, activity := range b.Activity {
			actor := activity.Actor
			if actor == "" {
				actor = "system"
			}
			fmt.Printf("\n[%s] %s · %s", activity.At, actor, activity.Type)
			if activity.Session != "" {
				fmt.Printf(" · session %s", activity.Session)
			}
			fmt.Println()
			if activity.Body != "" {
				fmt.Println(activity.Body)
			} else if len(activity.Data) > 0 {
				data, _ := json.Marshal(activity.Data)
				fmt.Println(string(data))
			}
		}
	}
}
