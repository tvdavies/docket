package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"slices"

	"github.com/tvdavies/docket/internal/task"
	"github.com/tvdavies/docket/internal/widget"
	"github.com/tvdavies/docket/internal/workspace"
)

func resolverGeneration(ws *workspace.Workspace) string {
	encoded, _ := json.Marshal(pluginsForBoard(ws))
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}
func annotateReferences(ws *workspace.Workspace, refs []task.Reference) []task.Reference {
	out := append([]task.Reference{}, refs...)
	generation := resolverGeneration(ws)
	for i := range out {
		out[i].ResolverID = ""
		out[i].ResolverGeneration = generation
		found := false
		for _, p := range ws.Plugins {
			for _, r := range p.Manifest.UI.ReferenceResolvers {
				if len(r.Kinds) > 0 && !slices.Contains(r.Kinds, out[i].Kind) {
					continue
				}
				pattern, err := regexp.Compile(r.Pattern)
				if err != nil || !pattern.MatchString(out[i].URL) {
					continue
				}
				out[i].ResolverID = r.ID
				found = true
				break
			}
			if found {
				break
			}
		}
	}
	return out
}
func projectBoardTask(ws *workspace.Workspace, summary *boardTask, index widget.Index) {
	records := index.Records(summary.ID)
	summary.WidgetSummaries = widget.Compact(records)
	summary.WidgetRevision = widget.Revision(records)
	summary.References = annotateReferences(ws, summary.References)
}
