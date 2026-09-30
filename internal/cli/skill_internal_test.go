package cli

import (
	"strings"
	"testing"

	"github.com/tvdavies/docket/docs"
)

// skillBudgetBytes bounds the entry guide loaded into every agent session. The
// previous all-in-one guide was about 8.7 KB; detail belongs in docs topics.
const skillBudgetBytes = 4096

func TestSkillGuideStaysWithinContextBudget(t *testing.T) {
	if len(skillDoc) > skillBudgetBytes {
		t.Fatalf("docket skill is %d bytes; keep it under %d and move detail to a docs topic", len(skillDoc), skillBudgetBytes)
	}
	for _, required := range []string{"--desc-file -", "--file -", "--help", "docket docs", "skill --full"} {
		if !strings.Contains(skillDoc, required) {
			t.Fatalf("docket skill no longer mentions %q", required)
		}
	}
	if _, _, ok := docs.Read(fullGuideTopic); !ok {
		t.Fatalf("full guide topic %q is not embedded", fullGuideTopic)
	}
}

// TestDocumentedCommandsMatchCobra checks every docket invocation in shell
// examples of the agent guides against the real command tree, so a renamed
// command or flag fails here instead of misleading an agent.
func TestDocumentedCommandsMatchCobra(t *testing.T) {
	guides := map[string]string{"internal/cli/skill.md": skillDoc}
	for _, name := range []string{fullGuideTopic, "cli"} {
		_, content, ok := docs.Read(name)
		if !ok {
			t.Fatalf("docs topic %q missing", name)
		}
		guides["docs/"+name+".md"] = content
	}
	for file, content := range guides {
		for _, line := range shellExampleLines(content) {
			checkDocumentedCommand(t, file, line)
		}
	}
}

func shellExampleLines(content string) []string {
	var lines []string
	inShell := false
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inShell = !inShell && (trimmed == "```sh" || trimmed == "```bash")
			continue
		}
		if !inShell {
			continue
		}
		if index := strings.Index(trimmed, "$(docket "); index >= 0 {
			trimmed = trimmed[index+2:]
			trimmed, _, _ = strings.Cut(trimmed, ")")
		}
		if strings.HasPrefix(trimmed, "docket ") {
			lines = append(lines, trimmed)
		}
	}
	return lines
}

func checkDocumentedCommand(t *testing.T, file, line string) {
	t.Helper()
	command := line
	for _, separator := range []string{" #", " |", " >", " &&"} {
		command, _, _ = strings.Cut(command, separator)
	}
	fields := strings.Fields(command)[1:]
	var words []string
	for _, field := range fields {
		if strings.HasPrefix(field, "-") || strings.HasPrefix(field, "[") {
			break
		}
		words = append(words, field)
	}
	if len(words) > 0 && words[0] == "COMMAND" {
		return
	}
	root := newRootCmd()
	target, _, err := root.Find(words)
	if err != nil {
		t.Errorf("%s: %q: %v", file, line, err)
		return
	}
	target.InitDefaultHelpFlag()
	for _, field := range fields {
		for _, part := range strings.Split(field, "|") {
			part = strings.Trim(part, "[]().,")
			if !strings.HasPrefix(part, "--") || part == "--" {
				continue
			}
			name, _, _ := strings.Cut(strings.TrimPrefix(part, "--"), "=")
			if target.Flags().Lookup(name) == nil && target.InheritedFlags().Lookup(name) == nil {
				t.Errorf("%s: %q: %s has no --%s flag", file, line, target.CommandPath(), name)
			}
		}
	}
}
