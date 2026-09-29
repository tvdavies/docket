package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/tvdavies/docket/internal/store"
	"github.com/tvdavies/docket/internal/task"
	"github.com/tvdavies/docket/internal/widget"
	"github.com/tvdavies/docket/internal/workspace"
)

type widgetWatermark struct {
	revision int64
	digest   [32]byte
}

func registerWidgetAPI(mux *http.ServeMux, manager *Manager, allowRemoteHost bool) {
	for _, operation := range []string{"create", "finalise"} {
		mux.HandleFunc("POST /api/workspaces/{workspace}/tasks/{task}/widgets/"+operation, func(w http.ResponseWriter, r *http.Request) {
			if !allowJSONMutation(w, r, allowRemoteHost) {
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, widget.MaxRecordBytes)
			var record widget.Record
			if !decodeJSONBody(w, r, &record) {
				return
			}
			phase := "created"
			if operation == "finalise" {
				phase = "finalised"
			}
			if record.Phase != phase {
				writeJSON(w, 400, map[string]string{"error": "invalid_widget_phase"})
				return
			}
			ws, release, ok := leaseAPIWorkspace(w, manager, r.PathValue("workspace"))
			if !ok {
				return
			}
			defer release()
			actions, capture := webMutationActions(ws, r)
			current, appended, err := actions.Widget(r.PathValue("workspace"), r.PathValue("task"), record)
			if err != nil {
				writeWidgetError(w, err)
				return
			}
			if current.Phase == "finalised" {
				if running := manager.runtimes[r.PathValue("workspace")]; running != nil {
					running.stream.removeWidget(record)
				}
			}
			status := http.StatusOK
			if appended && operation == "create" {
				status = http.StatusCreated
			}
			writeJSON(w, status, map[string]any{"record": current, "cursor": capture.token(manager, r.PathValue("workspace"))})
		})
	}
}
func writeWidgetError(w http.ResponseWriter, err error) {
	var e *widget.Error
	if !errors.As(err, &e) {
		writeAPIError(w, err)
		return
	}
	status := 400
	switch e.Code {
	case "widget_conflict", "widget_finalised":
		status = 409
	case "widget_not_created":
		status = 404
	case "widget_not_enabled":
		status = 403
	case "widget_cache_full":
		status = 429
	}
	writeJSON(w, status, e)
}
func (stream *workspaceStream) removeWidget(record widget.Record) {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	key := liveKey(livePayload{Kind: record.WidgetType, Task: record.TaskID, Session: record.InstanceID})
	delete(stream.live, key)
	delete(stream.widgets, key) // The durable terminal record now owns the fence.
}
func declaredWidget(ws *workspace.Workspace, kind string) bool {
	return widget.Enabled(ws, kind)
}
func (stream *workspaceStream) ingestWidget(ws *workspace.Workspace, input livePayload) (time.Time, error) {
	var expires time.Time
	if !widget.Enabled(ws, input.Kind) {
		return expires, &widget.Error{Code: "widget_not_enabled"}
	}
	p, err := widget.ParsePreview(input.Payload)
	if err != nil {
		return expires, &widget.Error{Code: "invalid_widget_preview"}
	}
	value, err := task.Load(ws, input.Task)
	if err != nil {
		return expires, err
	}
	err = store.WithLock(value.LockFile(), func() error {
		index, err := widget.Load(ws)
		if err != nil {
			return err
		}
		record, exists := index[widget.Key(widget.Record{TaskID: input.Task, WidgetType: input.Kind, InstanceID: input.Session})]
		if !exists {
			return &widget.Error{Code: "widget_not_created"}
		}
		if record.Current.Phase == "finalised" {
			return &widget.Error{Code: "widget_finalised", Revision: record.Current.Revision}
		}
		canonical, err := canonicalJSON(input.Payload)
		if err != nil {
			return &widget.Error{Code: "invalid_widget_preview"}
		}
		digest := sha256.Sum256(canonical)
		key := liveKey(input)
		stream.mu.Lock()
		old, known := stream.widgets[key]
		if p.Revision <= record.Current.Revision || (known && (p.Revision < old.revision || (p.Revision == old.revision && digest != old.digest))) {
			stream.mu.Unlock()
			return &widget.Error{Code: "widget_conflict", Revision: max(old.revision, record.Current.Revision)}
		}
		if !known && len(stream.widgets) >= maxLiveEntries {
			stream.mu.Unlock()
			return &widget.Error{Code: "widget_cache_full"}
		}
		stream.mu.Unlock()
		expires, err = stream.ingestLive(input, time.Duration(input.TTLMS)*time.Millisecond)
		if err == nil {
			stream.mu.Lock()
			stream.widgets[key] = widgetWatermark{revision: p.Revision, digest: digest}
			stream.mu.Unlock()
		}
		return err
	})
	return expires, err
}
func canonicalJSON(raw []byte) ([]byte, error) {
	var v any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&v); err != nil {
		return nil, err
	}
	return json.Marshal(v)
}
