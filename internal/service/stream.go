package service

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/tvdavies/docket/internal/events"
	"github.com/tvdavies/docket/internal/task"
	"github.com/tvdavies/docket/internal/workspace"
)

const (
	streamSubscriberBuffer = 128
	maxLiveEntries         = 2048
	maxLivePayloadBytes    = 64 << 10
	maxLiveTTL             = 10 * time.Minute
)

var streamHeartbeatInterval = 25 * time.Second
var streamWriteTimeout = 10 * time.Second
var errWorkspaceStreamUnavailable = errors.New("workspace stream is unavailable")

type streamConfig struct {
	Statuses []string `json:"statuses"`
	Terminal []string `json:"terminal"`
	Labels   []string `json:"labels"`
}

type streamInit struct {
	Workspace string       `json:"workspace"`
	Config    streamConfig `json:"config"`
	Tasks     []boardTask  `json:"tasks"`
	Cursor    string       `json:"cursor"`
}

type streamPatch struct {
	Event events.Event `json:"event"`
	Task  *boardTask   `json:"task,omitempty"`
}

type livePayload struct {
	Kind    string          `json:"kind"`
	Task    string          `json:"task,omitempty"`
	Session string          `json:"session,omitempty"`
	Payload json.RawMessage `json:"payload"`
	TTLMS   int64           `json:"ttl_ms"`
}

type liveItem struct {
	payload   livePayload
	expiresAt time.Time
	version   uint64
}

type streamNotification struct {
	kind        string
	cursor      string
	generation  string
	offset      int64
	data        any
	expiresAt   time.Time
	liveVersion uint64
}

type publicCursor struct {
	Version    int    `json:"v"`
	Generation string `json:"g"`
	Offset     int64  `json:"o"`
	PrefixHash string `json:"p,omitempty"`
}

type workspaceStream struct {
	mu          sync.Mutex
	generation  string
	head        int64
	config      streamConfig
	configValue string
	subscribers map[chan streamNotification]struct{}
	live        map[string]liveItem
	liveVersion uint64
	closed      bool
}

func newWorkspaceStream() *workspaceStream {
	return &workspaceStream{
		generation:  newStreamGeneration(),
		subscribers: map[chan streamNotification]struct{}{},
		live:        map[string]liveItem{},
	}
}

func newStreamGeneration() string {
	value := make([]byte, 12)
	if _, err := rand.Read(value); err == nil {
		return hex.EncodeToString(value)
	}
	return fmt.Sprintf("%x", time.Now().UnixNano())
}

func encodeStreamCursor(generation string, cursor events.LogCursor) string {
	data, _ := json.Marshal(publicCursor{Version: 1, Generation: generation, Offset: cursor.Offset, PrefixHash: cursor.PrefixHash})
	return base64.RawURLEncoding.EncodeToString(data)
}

func decodeStreamCursor(value string) (publicCursor, error) {
	if len(value) == 0 || len(value) > 1024 || strings.ContainsAny(value, "\r\n") {
		return publicCursor{}, fmt.Errorf("invalid stream cursor")
	}
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return publicCursor{}, fmt.Errorf("invalid stream cursor: %w", err)
	}
	var cursor publicCursor
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cursor); err != nil || cursor.Version != 1 || cursor.Generation == "" || cursor.Offset < 0 || (cursor.Offset > 0 && len(cursor.PrefixHash) != 64) {
		return publicCursor{}, fmt.Errorf("invalid stream cursor")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return publicCursor{}, fmt.Errorf("invalid stream cursor")
	}
	return cursor, nil
}

func (stream *workspaceStream) rotateLocked() {
	stream.generation = newStreamGeneration()
	stream.head = 0
}

func (stream *workspaceStream) observe(cursor events.LogCursor, reset bool) (string, string) {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if reset {
		stream.rotateLocked()
		for channel := range stream.subscribers {
			delete(stream.subscribers, channel)
			close(channel)
		}
	}
	if cursor.Offset > stream.head {
		stream.head = cursor.Offset
	}
	return stream.generation, encodeStreamCursor(stream.generation, cursor)
}

func (stream *workspaceStream) cursor(cursor events.LogCursor) (generation, token string) {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	return stream.generation, encodeStreamCursor(stream.generation, cursor)
}

func (stream *workspaceStream) generationMatches(value string) bool {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	return !stream.closed && stream.generation == value
}

func (stream *workspaceStream) subscribe() (<-chan streamNotification, func(), bool) {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if stream.closed {
		return nil, func() {}, false
	}
	channel := make(chan streamNotification, streamSubscriberBuffer)
	stream.subscribers[channel] = struct{}{}
	cancel := func() {
		stream.mu.Lock()
		if _, ok := stream.subscribers[channel]; ok {
			delete(stream.subscribers, channel)
			close(channel)
		}
		stream.mu.Unlock()
	}
	return channel, cancel, true
}

func (stream *workspaceStream) publish(notification streamNotification) {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if stream.closed {
		return
	}
	for channel := range stream.subscribers {
		select {
		case channel <- notification:
		default:
			delete(stream.subscribers, channel)
			close(channel)
		}
	}
}

func (stream *workspaceStream) publishPatch(record events.LogRecord, summary *boardTask) {
	generation, cursor := stream.observe(events.LogCursor{Offset: record.Offset, PrefixHash: record.PrefixHash}, record.Reset)
	stream.publish(streamNotification{
		kind: "patch", cursor: cursor, generation: generation, offset: record.Offset,
		data: streamPatch{Event: record.Event, Task: summary},
	})
}

func (stream *workspaceStream) setConfig(config streamConfig) {
	encoded, _ := json.Marshal(config)
	value := string(encoded)
	stream.mu.Lock()
	if stream.configValue == value {
		stream.mu.Unlock()
		return
	}
	stream.config = config
	stream.configValue = value
	stream.mu.Unlock()
	stream.publish(streamNotification{kind: "config", data: config})
}

func (stream *workspaceStream) currentConfig() streamConfig {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	return stream.config
}

func liveKey(payload livePayload) string {
	return payload.Kind + "\x00" + payload.Task + "\x00" + payload.Session
}

func (stream *workspaceStream) ingestLive(payload livePayload, ttl time.Duration) (time.Time, error) {
	now := time.Now().UTC()
	expiresAt := now.Add(ttl)
	payload.TTLMS = ttl.Milliseconds()
	key := liveKey(payload)
	stream.mu.Lock()
	if stream.closed {
		stream.mu.Unlock()
		return time.Time{}, errWorkspaceStreamUnavailable
	}
	stream.pruneLiveLocked(now)
	if _, exists := stream.live[key]; !exists && len(stream.live) >= maxLiveEntries {
		stream.mu.Unlock()
		return time.Time{}, fmt.Errorf("live item limit reached")
	}
	stream.liveVersion++
	version := stream.liveVersion
	stream.live[key] = liveItem{payload: payload, expiresAt: expiresAt, version: version}
	stream.mu.Unlock()
	stream.publish(streamNotification{kind: "live", data: payload, expiresAt: expiresAt, liveVersion: version})
	return expiresAt, nil
}

func (stream *workspaceStream) pruneLiveLocked(now time.Time) {
	for key, item := range stream.live {
		if !item.expiresAt.After(now) {
			delete(stream.live, key)
		}
	}
}

func (stream *workspaceStream) liveSnapshot() ([]streamNotification, uint64) {
	now := time.Now().UTC()
	stream.mu.Lock()
	defer stream.mu.Unlock()
	stream.pruneLiveLocked(now)
	result := make([]streamNotification, 0, len(stream.live))
	for _, item := range stream.live {
		result = append(result, streamNotification{kind: "live", data: item.payload, expiresAt: item.expiresAt, liveVersion: item.version})
	}
	return result, stream.liveVersion
}

func (stream *workspaceStream) restart() {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if stream.closed {
		return
	}
	stream.rotateLocked()
	for channel := range stream.subscribers {
		delete(stream.subscribers, channel)
		close(channel)
	}
}

func (stream *workspaceStream) close() {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if stream.closed {
		return
	}
	stream.closed = true
	for channel := range stream.subscribers {
		delete(stream.subscribers, channel)
		close(channel)
	}
	stream.live = nil
}

func configForStream(ws *workspace.Workspace) streamConfig {
	return streamConfig{
		Statuses: nonNilStrings(ws.Config.Statuses),
		Terminal: nonNilStrings(ws.Config.Terminal),
		Labels:   nonNilStrings(ws.Config.Labels),
	}
}

func (manager *Manager) streamRuntime(name string) (*runtime, error) {
	manager.mu.RLock()
	running, ok := manager.runtimes[name]
	manager.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrWorkspaceNotManaged, name)
	}
	return running, nil
}

// cursorForLeasedMutation reads the runtime while the caller holds the manager
// read lease returned by LeaseWorkspace. Taking a nested RLock here could
// deadlock behind a waiting registry writer.
func (manager *Manager) cursorForLeasedMutation(name string, cursor events.LogCursor) string {
	running, ok := manager.runtimes[name]
	if !ok || running.stream == nil {
		return ""
	}
	_, token := running.stream.cursor(cursor)
	return token
}

func (manager *Manager) publishTaskEvent(running *runtime, record events.LogRecord) error {
	if record.Event.Task == "" {
		running.stream.publishPatch(record, nil)
		return nil
	}
	ws, err := workspace.OpenRoot(running.entry.Path)
	if err != nil {
		return err
	}
	value, err := task.Load(ws, record.Event.Task)
	if err != nil {
		return err
	}
	summary, err := summariseTask(value)
	if err != nil {
		return err
	}
	running.stream.publishPatch(record, &summary)
	return nil
}

func registerStreamAPI(mux *http.ServeMux, manager *Manager, allowRemoteHost bool) {
	mux.HandleFunc("GET /api/workspaces/{workspace}/stream", func(writer http.ResponseWriter, request *http.Request) {
		running, err := manager.streamRuntime(request.PathValue("workspace"))
		if err != nil {
			writeAPIError(writer, err)
			return
		}
		serveWorkspaceStream(writer, request, running)
	})
	mux.HandleFunc("POST /api/workspaces/{workspace}/live", func(writer http.ResponseWriter, request *http.Request) {
		if !allowJSONMutation(writer, request, allowRemoteHost) {
			return
		}
		var input livePayload
		if !decodeJSONBody(writer, request, &input) {
			return
		}
		input.Kind = strings.TrimSpace(input.Kind)
		input.Task = strings.TrimSpace(input.Task)
		input.Session = strings.TrimSpace(input.Session)
		if !validLiveKind(input.Kind) {
			writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "invalid live kind"})
			return
		}
		if len(input.Task) > 200 || len(input.Session) > 200 {
			writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "task and session must be no longer than 200 characters"})
			return
		}
		if input.TTLMS <= 0 || input.TTLMS > maxLiveTTL.Milliseconds() {
			writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "ttl_ms must be between 1 and 600000"})
			return
		}
		if len(input.Payload) == 0 || len(input.Payload) > maxLivePayloadBytes || !json.Valid(input.Payload) {
			writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "payload must be valid JSON no larger than 64 KiB"})
			return
		}
		running, err := manager.streamRuntime(request.PathValue("workspace"))
		if err != nil {
			writeAPIError(writer, err)
			return
		}
		expiresAt, err := running.stream.ingestLive(input, time.Duration(input.TTLMS)*time.Millisecond)
		if err != nil {
			status := http.StatusTooManyRequests
			if errors.Is(err, errWorkspaceStreamUnavailable) {
				status = http.StatusServiceUnavailable
			}
			writeJSON(writer, status, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(writer, http.StatusAccepted, map[string]any{"ok": true, "expires_at": expiresAt.Format(time.RFC3339Nano)})
	})
}

func validLiveKind(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 100 {
		return false
	}
	for _, character := range value {
		if unicode.IsLetter(character) || unicode.IsDigit(character) || character == '.' || character == '-' || character == '_' || character == '/' {
			continue
		}
		return false
	}
	return true
}

func serveWorkspaceStream(writer http.ResponseWriter, request *http.Request, running *runtime) {
	if _, ok := writer.(http.Flusher); !ok {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "streaming is not supported"})
		return
	}
	channel, unsubscribe, ok := running.stream.subscribe()
	if !ok {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"error": "workspace stream is unavailable"})
		return
	}
	defer unsubscribe()
	ws, err := workspace.OpenRoot(running.entry.Path)
	if err != nil {
		writeAPIError(writer, err)
		return
	}

	writer.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-cache, no-transform")
	writer.Header().Set("Connection", "keep-alive")
	writer.Header().Set("X-Accel-Buffering", "no")
	if err := writeStreamFrame(writer, func() error {
		_, err := fmt.Fprint(writer, "retry: 1000\n\n")
		return err
	}); err != nil {
		return
	}

	requested := strings.TrimSpace(request.URL.Query().Get("cursor"))
	if requested == "" {
		requested = strings.TrimSpace(request.Header.Get("Last-Event-ID"))
	}
	replayEnd := int64(0)
	generation := ""
	resumed := false
	if requested != "" {
		if cursor, decodeErr := decodeStreamCursor(requested); decodeErr == nil && running.stream.generationMatches(cursor.Generation) {
			logCursor := events.LogCursor{Offset: cursor.Offset, PrefixHash: cursor.PrefixHash}
			if validationErr := events.ValidateLogCursor(ws, logCursor); validationErr == nil {
				records, end, readErr := events.ReadFromOffset(ws, cursor.Offset)
				if readErr == nil && running.stream.generationMatches(cursor.Generation) {
					generation = cursor.Generation
					replayEnd = end
					resumed = true
					if err := writeSSE(writer, "config", "", running.stream.currentConfig()); err != nil {
						return
					}
					for _, record := range records {
						if err := writeReplayPatch(writer, ws, generation, record); err != nil {
							return
						}
					}
				}
			}
		}
	}
	if !resumed {
		current, cursorErr := events.CurrentLogCursor(ws)
		if cursorErr != nil {
			return
		}
		var cursor string
		generation, cursor = running.stream.cursor(current)
		replayEnd = current.Offset
		initial, buildErr := buildStreamInit(ws, running.entry.Name, cursor)
		if buildErr != nil {
			return
		}
		if err := writeSSE(writer, "init", cursor, initial); err != nil {
			return
		}
	}
	liveSnapshot, liveSnapshotVersion := running.stream.liveSnapshot()
	for _, notification := range liveSnapshot {
		payload, ok := prepareLivePayload(notification, time.Now())
		if !ok {
			continue
		}
		if err := writeSSE(writer, "live", "", payload); err != nil {
			return
		}
	}

	heartbeat := time.NewTicker(streamHeartbeatInterval)
	defer heartbeat.Stop()
	for {
		select {
		case <-request.Context().Done():
			return
		case notification, open := <-channel:
			if !open {
				return
			}
			if notification.kind == "patch" && notification.generation == generation && notification.offset <= replayEnd {
				continue
			}
			if notification.kind == "patch" {
				generation = notification.generation
				replayEnd = notification.offset
			}
			data := notification.data
			if notification.kind == "live" {
				payload, version, ok := prepareLiveNotification(notification, liveSnapshotVersion, time.Now())
				if !ok {
					continue
				}
				data = payload
				liveSnapshotVersion = version
			}
			if err := writeSSE(writer, notification.kind, notification.cursor, data); err != nil {
				return
			}
		case <-heartbeat.C:
			if err := writeStreamFrame(writer, func() error {
				_, err := fmt.Fprint(writer, ": ping\n\n")
				return err
			}); err != nil {
				return
			}
		}
	}
}

func prepareLivePayload(notification streamNotification, now time.Time) (livePayload, bool) {
	if !notification.expiresAt.After(now) {
		return livePayload{}, false
	}
	payload, ok := notification.data.(livePayload)
	if !ok {
		return livePayload{}, false
	}
	payload.TTLMS = max(1, notification.expiresAt.Sub(now).Milliseconds())
	return payload, true
}

func prepareLiveNotification(notification streamNotification, snapshotVersion uint64, now time.Time) (livePayload, uint64, bool) {
	if notification.liveVersion <= snapshotVersion {
		return livePayload{}, snapshotVersion, false
	}
	payload, ok := prepareLivePayload(notification, now)
	if !ok {
		return livePayload{}, snapshotVersion, false
	}
	return payload, notification.liveVersion, true
}

func buildStreamInit(ws *workspace.Workspace, workspaceName, cursor string) (streamInit, error) {
	values, err := task.All(ws)
	if err != nil {
		return streamInit{}, err
	}
	result := streamInit{
		Workspace: workspaceName,
		Config:    configForStream(ws),
		Tasks:     make([]boardTask, 0, len(values)),
		Cursor:    cursor,
	}
	for _, value := range values {
		summary, err := summariseTask(value)
		if err != nil {
			return streamInit{}, err
		}
		result.Tasks = append(result.Tasks, summary)
	}
	return result, nil
}

func writeReplayPatch(writer http.ResponseWriter, ws *workspace.Workspace, generation string, record events.LogRecord) error {
	var summary *boardTask
	if record.Event.Task != "" {
		value, err := task.Load(ws, record.Event.Task)
		if err != nil {
			return err
		}
		card, err := summariseTask(value)
		if err != nil {
			return err
		}
		summary = &card
	}
	return writeSSE(writer, "patch", encodeStreamCursor(generation, events.LogCursor{Offset: record.Offset, PrefixHash: record.PrefixHash}), streamPatch{Event: record.Event, Task: summary})
}

func writeSSE(writer http.ResponseWriter, eventType, id string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if strings.ContainsAny(id, "\r\n") {
		return errors.New("invalid SSE id")
	}
	return writeStreamFrame(writer, func() error {
		if id != "" {
			if _, err := fmt.Fprintf(writer, "id: %s\n", id); err != nil {
				return err
			}
		}
		if eventType != "" {
			if _, err := fmt.Fprintf(writer, "event: %s\n", eventType); err != nil {
				return err
			}
		}
		_, err := fmt.Fprintf(writer, "data: %s\n\n", data)
		return err
	})
}

func writeStreamFrame(writer http.ResponseWriter, write func() error) error {
	controller := http.NewResponseController(writer)
	deadlineSet := true
	if err := controller.SetWriteDeadline(time.Now().Add(streamWriteTimeout)); err != nil {
		if !errors.Is(err, http.ErrNotSupported) {
			return err
		}
		deadlineSet = false
	}
	if err := write(); err != nil {
		return err
	}
	if err := controller.Flush(); err != nil {
		return err
	}
	if deadlineSet {
		if err := controller.SetWriteDeadline(time.Time{}); err != nil {
			return err
		}
	}
	return nil
}
