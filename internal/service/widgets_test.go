package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/tvdavies/docket/internal/actions"
	"github.com/tvdavies/docket/internal/plugin"
	"github.com/tvdavies/docket/internal/task"
	"github.com/tvdavies/docket/internal/widget"
	"github.com/tvdavies/docket/internal/workspace"
)

func TestWidgetPreviewsHaveZeroLedgerGrowth(t *testing.T) {
	ws, err := workspace.Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ws.Plugins = []workspace.LoadedPlugin{{Manifest: &plugin.Manifest{Name: "fixture", UI: plugin.UI{APIVersion: 2, Cards: []plugin.Card{{Type: "fixture/progress", Locations: []string{"board", "activity"}}}}}}}
	value, err := task.Create(ws, task.CreateOptions{Title: "Isolated live"})
	if err != nil {
		t.Fatal(err)
	}
	record := widget.Record{Version: 1, WidgetType: "fixture/progress", TaskID: value.ID, InstanceID: "sample", Revision: 1, Phase: "created", CreatedAt: "2026-09-10T00:00:00Z", Fallback: widget.Fallback{Label: "Job", StatusLabel: "Active", Priority: "active"}}
	ops := actions.Tasks{Workspace: ws}
	if _, _, err := ops.Widget("demo", value.ID, record); err != nil {
		t.Fatal(err)
	}
	stream := newWorkspaceStream()
	defer stream.close()
	before, _ := os.ReadFile(ws.EventsFile())
	input := livePayload{Kind: record.WidgetType, Task: value.ID, Session: record.InstanceID, TTLMS: 30000}
	for revision := 2; revision <= 1001; revision++ {
		input.Payload = json.RawMessage(fmt.Sprintf(`{"widget_version":1,"revision":%d,"data":{"version":1,"value":{"count":%d}}}`, revision, revision))
		if _, err := stream.ingestWidget(ws, input); err != nil {
			t.Fatalf("frame %d: %v", revision, err)
		}
	}
	after, _ := os.ReadFile(ws.EventsFile())
	if !bytes.Equal(before, after) {
		t.Fatal("preview appended ledger bytes")
	}
	t.Logf("1000 previews: ledger unchanged at %d bytes; cache entries=%d", len(after), len(stream.live))
	original := stream.live[liveKey(input)]
	if _, err := stream.ingestWidget(ws, input); err != nil {
		t.Fatal("heartbeat", err)
	}
	if !stream.live[liveKey(input)].expiresAt.After(original.expiresAt) {
		t.Fatal("heartbeat did not renew")
	}
	current := stream.live[liveKey(input)]
	bad := input
	bad.Payload = bytes.Replace(input.Payload, []byte(`"count":1001`), []byte(`"count":1002`), 1)
	if _, err := stream.ingestWidget(ws, bad); err == nil {
		t.Fatal("same revision conflict accepted")
	}
	bad.Payload = bytes.Replace(input.Payload, []byte(`"revision":1001`), []byte(`"revision":999`), 1)
	if _, err := stream.ingestWidget(ws, bad); err == nil {
		t.Fatal("older revision accepted")
	}
	if !current.expiresAt.Equal(stream.live[liveKey(input)].expiresAt) {
		t.Fatal("rejected frame renewed freshness")
	}
	stream.pruneLiveLocked(time.Now().Add(time.Hour))
	if _, err := stream.ingestWidget(ws, bad); err == nil {
		t.Fatal("expired high water lost")
	}
	bad = input
	bad.Session = "unknown"
	if _, err := stream.ingestWidget(ws, bad); err == nil {
		t.Fatal("unknown instance accepted")
	}
	record.Phase = "finalised"
	record.Revision = 1002
	if _, _, err := ops.Widget("demo", value.ID, record); err != nil {
		t.Fatal(err)
	}
	stream.removeWidget(record)
	input.Payload = bytes.Replace(input.Payload, []byte(`"revision":1001`), []byte(`"revision":1003`), 1)
	if _, err := stream.ingestWidget(ws, input); err == nil {
		t.Fatal("terminal fence bypassed")
	}
	if len(stream.live) != 0 {
		t.Fatal("finalised preview retained")
	}
}
func TestResolverProjectionUsesOrderedGoPatternsAndWorkspaceMetadata(t *testing.T) {
	one := &workspace.Workspace{Plugins: []workspace.LoadedPlugin{{Manifest: &plugin.Manifest{Name: "first", UI: plugin.UI{ReferenceResolvers: []plugin.ReferenceResolver{{ID: "first/ref", Pattern: `\Ahttps://example\.com/`, Kinds: []string{"plan"}}}}, Service: &plugin.Service{URL: "http://localhost:1"}}}, {Manifest: &plugin.Manifest{Name: "second", UI: plugin.UI{ReferenceResolvers: []plugin.ReferenceResolver{{ID: "second/ref", Pattern: `.*`}}}}}}}
	two := &workspace.Workspace{Plugins: one.Plugins[1:]}
	refs := []task.Reference{{ID: "r", Kind: "plan", URL: "https://example.com/test", Title: "Source"}}
	a, b := annotateReferences(one, refs), annotateReferences(two, refs)
	if a[0].ResolverID != "first/ref" || b[0].ResolverID != "second/ref" || a[0].ResolverGeneration == b[0].ResolverGeneration || refs[0].ResolverID != "" {
		t.Fatalf("bad projections: %+v %+v", a, b)
	}
	if pluginsForBoard(one)[0].ServiceBase != "/plugins/first" || pluginsForBoard(two)[0].ServiceBase != "" {
		t.Fatal("fabricated service")
	}
}
