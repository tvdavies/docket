package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/tvdavies/docket/internal/bundle"
)

func readStdin() ([]byte, error) {
	return io.ReadAll(os.Stdin)
}

// printBundleHuman renders a full context bundle as readable markdown-ish text.
func printBundleHuman(b *bundle.Bundle) {
	printStateHuman(b.State())
	printActivityHuman(b.ID, b.Activity, b.ActivityPage, b.CommentsOmitted)
}

// printContextHuman renders the current or agent view. The current view
// prints only a pointer to the history it left out.
func printContextHuman(c *bundle.Context) {
	printStateHuman(c)
	if c.View == bundle.ViewCurrent {
		if c.ActivityPage.Total > 0 {
			fmt.Printf("\n## Activity\n%d items not shown; read them with: docket show %s --view agent\n", c.ActivityPage.Total, c.ID)
		}
		return
	}
	printActivityHuman(c.ID, c.Activity, &c.ActivityPage, c.CommentsOmitted)
}

func printStateHuman(b *bundle.Context) {
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
		for _, kind := range slices.Sorted(maps.Keys(b.Relationships)) {
			refs := b.Relationships[kind]
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
}

func printActivityHuman(id string, activity []bundle.ActivityView, page *bundle.ActivityPage, commentsOmitted int) {
	if page == nil {
		if len(activity) > 0 {
			fmt.Printf("\n## Activity (%d)\n", len(activity))
		}
	} else if page.Total > 0 {
		fmt.Printf("\n## Activity (%d–%d of %d)\n", page.Start+1, page.End, page.Total)
		if page.NextBefore > 0 {
			fmt.Printf("%d older items not shown; read them with: docket show %s --view agent --activity-before %d\n", page.NextBefore, id, page.NextBefore)
		}
		if newer := page.Total - page.End; newer > 0 {
			fmt.Printf("%d newer items not shown.\n", newer)
		}
	}
	if commentsOmitted > 0 {
		fmt.Printf("%d older comments omitted by --comments.\n", commentsOmitted)
	}
	for _, item := range activity {
		actor := item.Actor
		if actor == "" {
			actor = "system"
		}
		fmt.Printf("\n[%s] %s · %s", item.At, actor, item.Type)
		if item.Session != "" {
			fmt.Printf(" · session %s", item.Session)
		}
		fmt.Println()
		if item.Body != "" {
			fmt.Println(item.Body)
		} else if len(item.Data) > 0 {
			data, _ := json.Marshal(item.Data)
			fmt.Println(string(data))
		}
	}
}
