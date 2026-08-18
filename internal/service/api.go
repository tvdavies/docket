package service

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tvdavies/docket/internal/actions"
	"github.com/tvdavies/docket/internal/bundle"
	"github.com/tvdavies/docket/internal/project"
	"github.com/tvdavies/docket/internal/task"
	"github.com/tvdavies/docket/internal/workspace"
)

const maxAPIRequestBytes = 1 << 20

type boardResponse struct {
	Workspace string      `json:"workspace"`
	Path      string      `json:"path"`
	Statuses  []string    `json:"statuses"`
	Terminal  []string    `json:"terminal"`
	Labels    []string    `json:"labels"`
	Tasks     []boardTask `json:"tasks"`
	UpdatedAt string      `json:"updated_at"`
}

type boardTask struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Status    string   `json:"status"`
	Project   string   `json:"project,omitempty"`
	Labels    []string `json:"labels"`
	Assignee  string   `json:"assignee,omitempty"`
	UpdatedAt string   `json:"updated_at"`
}

type createTaskRequest struct {
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Project     string   `json:"project"`
	Labels      []string `json:"labels"`
	Assignee    string   `json:"assignee"`
	Status      string   `json:"status"`
}

type updateTaskRequest struct {
	Title       *string   `json:"title"`
	Description *string   `json:"description"`
	Assignee    *string   `json:"assignee"`
	Labels      *[]string `json:"labels"`
	Status      *string   `json:"status"`
}

type commentRequest struct {
	Text string `json:"text"`
}

func registerAPI(mux *http.ServeMux, manager *Manager, allowRemoteHost bool) {
	mux.HandleFunc("GET /healthz", func(writer http.ResponseWriter, request *http.Request) {
		writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "workspaces": len(manager.Statuses())})
	})
	mux.HandleFunc("GET /api/workspaces", func(writer http.ResponseWriter, request *http.Request) {
		writeJSON(writer, http.StatusOK, manager.Statuses())
	})
	mux.HandleFunc("GET /api/workspaces/{workspace}/board", func(writer http.ResponseWriter, request *http.Request) {
		ws, release, ok := leaseAPIWorkspace(writer, manager, request.PathValue("workspace"))
		if !ok {
			return
		}
		defer release()
		tasks, err := task.All(ws)
		if err != nil {
			writeAPIError(writer, err)
			return
		}
		result := boardResponse{
			Workspace: request.PathValue("workspace"),
			Path:      ws.Root,
			Statuses:  nonNilStrings(ws.Config.Statuses),
			Terminal:  nonNilStrings(ws.Config.Terminal),
			Labels:    nonNilStrings(ws.Config.Labels),
			Tasks:     make([]boardTask, 0, len(tasks)),
			UpdatedAt: time.Now().UTC().Format(time.RFC3339),
		}
		for _, value := range tasks {
			result.Tasks = append(result.Tasks, summariseTask(value))
		}
		writeJSON(writer, http.StatusOK, result)
	})
	mux.HandleFunc("POST /api/workspaces/{workspace}/tasks", func(writer http.ResponseWriter, request *http.Request) {
		if !allowJSONMutation(writer, request, allowRemoteHost) {
			return
		}
		var input createTaskRequest
		if !decodeJSONBody(writer, request, &input) {
			return
		}
		ws, release, ok := leaseAPIWorkspace(writer, manager, request.PathValue("workspace"))
		if !ok {
			return
		}
		defer release()
		input.Labels = normaliseLabels(input.Labels)
		if input.Project != "" {
			if _, err := project.Load(ws, input.Project); err != nil {
				writeAPIError(writer, err)
				return
			}
		}
		operations := webTaskActions(ws, request)
		created, err := operations.Create(task.CreateOptions{
			Title:       input.Title,
			Description: input.Description,
			Project:     input.Project,
			Labels:      input.Labels,
			Assignee:    input.Assignee,
			Status:      input.Status,
		})
		if err != nil {
			writeAPIError(writer, err)
			return
		}
		writeTaskBundle(writer, http.StatusCreated, ws, created.ID)
	})
	mux.HandleFunc("GET /api/workspaces/{workspace}/tasks/{task}", func(writer http.ResponseWriter, request *http.Request) {
		ws, release, ok := leaseAPIWorkspace(writer, manager, request.PathValue("workspace"))
		if !ok {
			return
		}
		defer release()
		writeTaskBundle(writer, http.StatusOK, ws, request.PathValue("task"))
	})
	mux.HandleFunc("PATCH /api/workspaces/{workspace}/tasks/{task}", func(writer http.ResponseWriter, request *http.Request) {
		if !allowJSONMutation(writer, request, allowRemoteHost) {
			return
		}
		var input updateTaskRequest
		if !decodeJSONBody(writer, request, &input) {
			return
		}
		ws, release, ok := leaseAPIWorkspace(writer, manager, request.PathValue("workspace"))
		if !ok {
			return
		}
		defer release()
		id := request.PathValue("task")
		if err := updateTaskFromAPI(ws, webTaskActions(ws, request), id, input); err != nil {
			writeAPIError(writer, err)
			return
		}
		writeTaskBundle(writer, http.StatusOK, ws, id)
	})
	mux.HandleFunc("POST /api/workspaces/{workspace}/tasks/{task}/comments", func(writer http.ResponseWriter, request *http.Request) {
		if !allowJSONMutation(writer, request, allowRemoteHost) {
			return
		}
		var input commentRequest
		if !decodeJSONBody(writer, request, &input) {
			return
		}
		ws, release, ok := leaseAPIWorkspace(writer, manager, request.PathValue("workspace"))
		if !ok {
			return
		}
		defer release()
		id := request.PathValue("task")
		if _, err := webTaskActions(ws, request).Comment(id, input.Text); err != nil {
			writeAPIError(writer, err)
			return
		}
		writeTaskBundle(writer, http.StatusCreated, ws, id)
	})
}

func updateTaskFromAPI(ws *workspace.Workspace, operations actions.Tasks, id string, input updateTaskRequest) error {
	_, err := operations.Patch(id, actions.PatchOptions{
		Title:       input.Title,
		Description: input.Description,
		Assignee:    input.Assignee,
		Labels:      input.Labels,
		Status:      input.Status,
	})
	return err
}

func webTaskActions(ws *workspace.Workspace, request *http.Request) actions.Tasks {
	actor := strings.TrimSpace(request.Header.Get("X-Docket-Actor"))
	if actor == "" {
		actor = "web"
	}
	if len(actor) > 100 {
		actor = actor[:100]
	}
	return actions.Tasks{Workspace: ws, Actor: actor, Session: "web"}
}

func leaseAPIWorkspace(writer http.ResponseWriter, manager *Manager, name string) (*workspace.Workspace, func(), bool) {
	ws, release, err := manager.LeaseWorkspace(name)
	if err != nil {
		writeAPIError(writer, err)
		return nil, nil, false
	}
	return ws, release, true
}

func writeTaskBundle(writer http.ResponseWriter, status int, ws *workspace.Workspace, id string) {
	result, err := bundle.Build(ws, id, 0)
	if err != nil {
		writeAPIError(writer, err)
		return
	}
	writeJSON(writer, status, result)
}

func summariseTask(value *task.Task) boardTask {
	return boardTask{
		ID: value.ID, Title: value.Title, Status: value.Status, Project: value.Project,
		Labels: nonNilStrings(value.Labels), Assignee: value.Assignee,
		UpdatedAt: value.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func allowJSONMutation(writer http.ResponseWriter, request *http.Request, allowRemoteHost bool) bool {
	if !allowRemoteHost && !isLoopbackRequestHost(request.Host) {
		writeJSON(writer, http.StatusForbidden, map[string]string{"error": "non-loopback Host is not allowed"})
		return false
	}
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeJSON(writer, http.StatusUnsupportedMediaType, map[string]string{"error": "Content-Type must be application/json"})
		return false
	}
	origin := request.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || !strings.EqualFold(parsed.Host, request.Host) {
		writeJSON(writer, http.StatusForbidden, map[string]string{"error": "cross-origin writes are not allowed"})
		return false
	}
	return true
}

func isLoopbackRequestHost(hostPort string) bool {
	host := hostPort
	if parsedHost, _, err := net.SplitHostPort(hostPort); err == nil {
		host = parsedHost
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func decodeJSONBody(writer http.ResponseWriter, request *http.Request, destination any) bool {
	request.Body = http.MaxBytesReader(writer, request.Body, maxAPIRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "request body must contain one JSON object"})
		return false
	}
	return true
}

func writeAPIError(writer http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	message := err.Error()
	switch {
	case errors.Is(err, ErrWorkspaceNotManaged), strings.Contains(message, "not found"):
		status = http.StatusNotFound
	case strings.Contains(message, "required"),
		strings.Contains(message, "invalid task id"),
		strings.Contains(message, "invalid project id"),
		strings.Contains(message, "cannot be empty"),
		strings.Contains(message, "unknown status"),
		strings.Contains(message, "no task changes"):
		status = http.StatusBadRequest
	}
	writeJSON(writer, status, map[string]string{"error": message})
}

func normaliseLabels(labels []string) []string {
	result := make([]string, 0, len(labels))
	seen := map[string]bool{}
	for _, label := range labels {
		label = strings.TrimSpace(label)
		if label != "" && !seen[label] {
			seen[label] = true
			result = append(result, label)
		}
	}
	return result
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
