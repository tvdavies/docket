package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/tvdavies/docket/internal/actions"
	"github.com/tvdavies/docket/internal/bundle"
	"github.com/tvdavies/docket/internal/plugin"
	"github.com/tvdavies/docket/internal/project"
	"github.com/tvdavies/docket/internal/registry"
	"github.com/tvdavies/docket/internal/session"
	"github.com/tvdavies/docket/internal/task"
	"github.com/tvdavies/docket/internal/workspace"
)

const (
	maxAPIRequestBytes = 1 << 20
	maxAttachmentBytes = 25 << 20
)

type boardResponse struct {
	Workspace string        `json:"workspace"`
	Path      string        `json:"path"`
	Statuses  []string      `json:"statuses"`
	Terminal  []string      `json:"terminal"`
	Labels    []string      `json:"labels"`
	Plugins   []boardPlugin `json:"plugins"`
	Tasks     []boardTask   `json:"tasks"`
	UpdatedAt string        `json:"updated_at"`
}

type boardPlugin struct {
	Name               string                     `json:"name"`
	Version            string                     `json:"version"`
	Cards              []plugin.Card              `json:"cards"`
	ReferenceResolvers []plugin.ReferenceResolver `json:"reference_resolvers"`
	ServiceBase        string                     `json:"service_base,omitempty"`
}

type pluginAPIEntry struct {
	Name        string                           `json:"name"`
	Version     string                           `json:"version"`
	Description string                           `json:"description,omitempty"`
	Source      registry.PluginSource            `json:"source"`
	Schemas     plugin.ConfigSchemas             `json:"schemas"`
	Values      map[string]any                   `json:"instance_values"`
	Workspaces  map[string]pluginWorkspaceValues `json:"workspace_values"`
}

type pluginWorkspaceValues struct {
	Config   map[string]any            `json:"config"`
	Statuses map[string]map[string]any `json:"statuses"`
}

type configPatchRequest struct {
	Values map[string]any `json:"values"`
}

type boardTask struct {
	ID         string           `json:"id"`
	Title      string           `json:"title"`
	Status     string           `json:"status"`
	Project    string           `json:"project,omitempty"`
	Labels     []string         `json:"labels"`
	Assignee   string           `json:"assignee,omitempty"`
	Wait       *task.Wait       `json:"wait,omitempty"`
	References []task.Reference `json:"references"`
	// ActiveSessions is a deprecated compatibility placeholder. Docket no
	// longer infers external process liveness from command-context pointers.
	ActiveSessions []session.Entry `json:"active_sessions"`
	CreatedAt      string          `json:"created_at"`
	UpdatedAt      string          `json:"updated_at"`
	ResourceCount  int             `json:"resource_count"`
}

type activityResponse struct {
	bundle.ActivityView
	BodyHTML string `json:"body_html,omitempty"`
}

type taskDetailResponse struct {
	*bundle.Bundle
	DescriptionHTML string             `json:"description_html"`
	Activity        []activityResponse `json:"activity"`
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

type setWaitRequest struct {
	Kind      string `json:"kind"`
	Reason    string `json:"reason"`
	Reference string `json:"reference"`
}

type resolveWaitRequest struct {
	WaitID string `json:"wait_id"`
	Result string `json:"result"`
}

type addReferenceRequest struct {
	Kind  string `json:"kind"`
	URL   string `json:"url"`
	Title string `json:"title"`
}

func registerAPI(mux *http.ServeMux, manager *Manager, allowRemoteHost bool) {
	mux.HandleFunc("GET /healthz", func(writer http.ResponseWriter, request *http.Request) {
		writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "workspaces": len(manager.Statuses())})
	})
	mux.HandleFunc("GET /api/workspaces", func(writer http.ResponseWriter, request *http.Request) {
		writeJSON(writer, http.StatusOK, manager.Statuses())
	})
	mux.HandleFunc("GET /api/plugins", func(writer http.ResponseWriter, request *http.Request) {
		config, err := registry.Load()
		if err != nil {
			writeAPIError(writer, err)
			return
		}
		result := make([]pluginAPIEntry, 0, len(config.Plugins))
		for _, entry := range config.Plugins {
			manifest, err := plugin.Load(entry.Path, plugin.EngineVersion)
			if err != nil {
				writeAPIError(writer, err)
				return
			}
			resolvedValues, err := manifest.ResolveInstanceConfig(entry.Config)
			if err != nil {
				writeAPIError(writer, err)
				return
			}
			values := map[string]any{}
			for key, value := range resolvedValues {
				if field, secret := manifest.Config.Instance[key]; secret && field.Secret {
					continue
				}
				values[key] = value
			}
			workspaceValues := map[string]pluginWorkspaceValues{}
			for _, workspaceEntry := range config.Workspaces {
				declared, err := workspace.LoadDeclaredRoot(workspaceEntry.Path)
				if err != nil {
					continue
				}
				use, enabled := declared.Plugins.Values[entry.Name]
				if !enabled {
					continue
				}
				configValues := use.Config
				if configValues == nil {
					configValues = map[string]any{}
				}
				statusValues := use.Statuses
				if statusValues == nil {
					statusValues = map[string]map[string]any{}
				}
				workspaceValues[workspaceEntry.Name] = pluginWorkspaceValues{
					Config: configValues, Statuses: statusValues,
				}
			}
			result = append(result, pluginAPIEntry{
				Name: manifest.Name, Version: manifest.Version, Description: manifest.Description,
				Source: entry.Source, Schemas: manifest.Config, Values: values, Workspaces: workspaceValues,
			})
		}
		writeJSON(writer, http.StatusOK, result)
	})
	mux.HandleFunc("PATCH /api/plugins/{plugin}/config", func(writer http.ResponseWriter, request *http.Request) {
		if !allowJSONMutation(writer, request, allowRemoteHost) {
			return
		}
		var input configPatchRequest
		if !decodeJSONBody(writer, request, &input) {
			return
		}
		if err := patchInstancePluginConfig(request.PathValue("plugin"), input.Values); err != nil {
			writeAPIError(writer, err)
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{"plugin": request.PathValue("plugin"), "values": input.Values})
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
			Plugins:   make([]boardPlugin, 0, len(ws.Plugins)),
			Tasks:     make([]boardTask, 0, len(tasks)),
			UpdatedAt: time.Now().UTC().Format(time.RFC3339),
		}
		for _, loaded := range ws.Plugins {
			metadata := boardPlugin{
				Name: loaded.Manifest.Name, Version: loaded.Manifest.Version,
				Cards:              append([]plugin.Card{}, loaded.Manifest.UI.Cards...),
				ReferenceResolvers: append([]plugin.ReferenceResolver{}, loaded.Manifest.UI.ReferenceResolvers...),
			}
			if loaded.Manifest.Service != nil {
				metadata.ServiceBase = "/plugins/" + loaded.Manifest.Name
			}
			result.Plugins = append(result.Plugins, metadata)
		}
		for _, value := range tasks {
			summary, err := summariseTask(value)
			if err != nil {
				writeAPIError(writer, err)
				return
			}
			result.Tasks = append(result.Tasks, summary)
		}
		writeJSON(writer, http.StatusOK, result)
	})
	mux.HandleFunc("PATCH /api/workspaces/{workspace}/plugins/{plugin}/config", func(writer http.ResponseWriter, request *http.Request) {
		if !allowJSONMutation(writer, request, allowRemoteHost) {
			return
		}
		var input configPatchRequest
		if !decodeJSONBody(writer, request, &input) {
			return
		}
		ws, release, ok := leaseAPIWorkspace(writer, manager, request.PathValue("workspace"))
		if !ok {
			return
		}
		defer release()
		if err := patchWorkspacePluginConfig(ws, request.PathValue("plugin"), "", input.Values); err != nil {
			writeAPIError(writer, err)
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{"plugin": request.PathValue("plugin"), "values": input.Values})
	})
	mux.HandleFunc("PATCH /api/workspaces/{workspace}/plugins/{plugin}/statuses/{status}", func(writer http.ResponseWriter, request *http.Request) {
		if !allowJSONMutation(writer, request, allowRemoteHost) {
			return
		}
		var input configPatchRequest
		if !decodeJSONBody(writer, request, &input) {
			return
		}
		ws, release, ok := leaseAPIWorkspace(writer, manager, request.PathValue("workspace"))
		if !ok {
			return
		}
		defer release()
		if err := patchWorkspacePluginConfig(ws, request.PathValue("plugin"), request.PathValue("status"), input.Values); err != nil {
			writeAPIError(writer, err)
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{"plugin": request.PathValue("plugin"), "status": request.PathValue("status"), "values": input.Values})
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
	mux.HandleFunc("PUT /api/workspaces/{workspace}/tasks/{task}/wait", func(writer http.ResponseWriter, request *http.Request) {
		if !allowJSONMutation(writer, request, allowRemoteHost) {
			return
		}
		var input setWaitRequest
		if !decodeJSONBody(writer, request, &input) {
			return
		}
		ws, release, ok := leaseAPIWorkspace(writer, manager, request.PathValue("workspace"))
		if !ok {
			return
		}
		defer release()
		id := request.PathValue("task")
		if _, err := webTaskActions(ws, request).SetWait(id, actions.SetWaitOptions{
			Kind: input.Kind, Reason: input.Reason, Reference: input.Reference,
		}); err != nil {
			writeAPIError(writer, err)
			return
		}
		writeTaskBundle(writer, http.StatusCreated, ws, id)
	})
	mux.HandleFunc("POST /api/workspaces/{workspace}/tasks/{task}/wait/resolve", func(writer http.ResponseWriter, request *http.Request) {
		if !allowJSONMutation(writer, request, allowRemoteHost) {
			return
		}
		var input resolveWaitRequest
		if !decodeJSONBody(writer, request, &input) {
			return
		}
		ws, release, ok := leaseAPIWorkspace(writer, manager, request.PathValue("workspace"))
		if !ok {
			return
		}
		defer release()
		id := request.PathValue("task")
		if _, err := webTaskActions(ws, request).ResolveWait(id, actions.ResolveWaitOptions{
			WaitID: input.WaitID, Result: input.Result,
		}); err != nil {
			writeAPIError(writer, err)
			return
		}
		writeTaskBundle(writer, http.StatusOK, ws, id)
	})
	mux.HandleFunc("POST /api/workspaces/{workspace}/tasks/{task}/references", func(writer http.ResponseWriter, request *http.Request) {
		if !allowJSONMutation(writer, request, allowRemoteHost) {
			return
		}
		var input addReferenceRequest
		if !decodeJSONBody(writer, request, &input) {
			return
		}
		ws, release, ok := leaseAPIWorkspace(writer, manager, request.PathValue("workspace"))
		if !ok {
			return
		}
		defer release()
		id := request.PathValue("task")
		if _, _, err := webTaskActions(ws, request).AddReference(id, input.Kind, input.URL, input.Title); err != nil {
			writeAPIError(writer, err)
			return
		}
		writeTaskBundle(writer, http.StatusCreated, ws, id)
	})
	mux.HandleFunc("DELETE /api/workspaces/{workspace}/tasks/{task}/references/{reference}", func(writer http.ResponseWriter, request *http.Request) {
		if !allowJSONMutation(writer, request, allowRemoteHost) {
			return
		}
		if !decodeEmptyJSONBody(writer, request) {
			return
		}
		ws, release, ok := leaseAPIWorkspace(writer, manager, request.PathValue("workspace"))
		if !ok {
			return
		}
		defer release()
		id := request.PathValue("task")
		if _, _, err := webTaskActions(ws, request).RemoveReference(id, request.PathValue("reference")); err != nil {
			writeAPIError(writer, err)
			return
		}
		writeTaskBundle(writer, http.StatusOK, ws, id)
	})
	mux.HandleFunc("POST /api/workspaces/{workspace}/tasks/{task}/attachments", func(writer http.ResponseWriter, request *http.Request) {
		if !allowMultipartMutation(writer, request, allowRemoteHost) {
			return
		}
		request.Body = http.MaxBytesReader(writer, request.Body, maxAttachmentBytes+maxAPIRequestBytes)
		if err := request.ParseMultipartForm(maxAttachmentBytes); err != nil {
			status := http.StatusBadRequest
			if strings.Contains(err.Error(), "request body too large") {
				status = http.StatusRequestEntityTooLarge
			}
			writeJSON(writer, status, map[string]string{"error": "invalid multipart upload: " + err.Error()})
			return
		}
		if request.MultipartForm != nil {
			defer request.MultipartForm.RemoveAll()
		}
		file, header, err := request.FormFile("file")
		if err != nil {
			writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "file is required"})
			return
		}
		defer file.Close()
		if header.Size > maxAttachmentBytes {
			writeJSON(writer, http.StatusRequestEntityTooLarge, map[string]string{"error": "attachment exceeds 25 MiB"})
			return
		}
		data, err := io.ReadAll(io.LimitReader(file, maxAttachmentBytes+1))
		if err != nil {
			writeAPIError(writer, err)
			return
		}
		if len(data) > maxAttachmentBytes {
			writeJSON(writer, http.StatusRequestEntityTooLarge, map[string]string{"error": "attachment exceeds 25 MiB"})
			return
		}
		ws, release, ok := leaseAPIWorkspace(writer, manager, request.PathValue("workspace"))
		if !ok {
			return
		}
		defer release()
		id := request.PathValue("task")
		if _, err := webTaskActions(ws, request).Attach(id, header.Filename, data, request.FormValue("caption")); err != nil {
			writeAPIError(writer, err)
			return
		}
		writeTaskBundle(writer, http.StatusCreated, ws, id)
	})
	mux.HandleFunc("GET /api/workspaces/{workspace}/tasks/{task}/attachments/{file}", func(writer http.ResponseWriter, request *http.Request) {
		ws, release, ok := leaseAPIWorkspace(writer, manager, request.PathValue("workspace"))
		if !ok {
			return
		}
		defer release()
		value, err := task.Load(ws, request.PathValue("task"))
		if err != nil {
			writeAPIError(writer, err)
			return
		}
		path, attachment, err := value.AttachmentPath(request.PathValue("file"))
		if err != nil {
			writeAPIError(writer, err)
			return
		}
		file, err := os.Open(path)
		if err != nil {
			writeAPIError(writer, err)
			return
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil {
			writeAPIError(writer, err)
			return
		}
		writer.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": attachment.File}))
		writer.Header().Set("Cache-Control", "no-store")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
		writer.Header().Set("Content-Type", attachment.Mime)
		http.ServeContent(writer, request, attachment.File, info.ModTime(), file)
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

func patchInstancePluginConfig(name string, values map[string]any) error {
	config, err := registry.Load()
	if err != nil {
		return err
	}
	var entry *registry.PluginEntry
	for index := range config.Plugins {
		if config.Plugins[index].Name == name {
			entry = &config.Plugins[index]
			break
		}
	}
	if entry == nil {
		return fmt.Errorf("plugin %q is not installed", name)
	}
	manifest, err := plugin.Load(entry.Path, plugin.EngineVersion)
	if err != nil {
		return err
	}
	candidate := map[string]any{}
	for key, value := range entry.Config {
		candidate[key] = value
	}
	for key, value := range values {
		candidate[key] = value
	}
	if _, err := manifest.ResolveInstanceConfig(candidate); err != nil {
		return err
	}
	for _, workspaceEntry := range config.Workspaces {
		declared, openErr := workspace.LoadDeclaredRoot(workspaceEntry.Path)
		if openErr != nil {
			return fmt.Errorf("validate workspace %q: %w", workspaceEntry.Name, openErr)
		}
		if _, enabled := declared.Plugins.Values[name]; !enabled {
			continue
		}
		if err := workspace.ValidatePluginCandidate(declared, manifest, candidate); err != nil {
			return fmt.Errorf("workspace %q: %w", workspaceEntry.Name, err)
		}
	}
	return registry.Update(func(latest *registry.Config) error {
		for index := range latest.Plugins {
			if latest.Plugins[index].Name == name {
				latest.Plugins[index].Config = candidate
				return nil
			}
		}
		return fmt.Errorf("plugin %q is not installed", name)
	})
}

func patchWorkspacePluginConfig(ws *workspace.Workspace, name, status string, values map[string]any) error {
	return workspace.MutateDeclaredConfig(ws.Root, func(declared *workspace.Config) error {
		use, enabled := declared.Plugins.Values[name]
		if !enabled {
			return fmt.Errorf("plugin %q is not enabled", name)
		}
		if status == "" {
			if use.Config == nil {
				use.Config = map[string]any{}
			}
			for key, value := range values {
				use.Config[key] = value
			}
		} else {
			if use.Statuses == nil {
				use.Statuses = map[string]map[string]any{}
			}
			current := use.Statuses[status]
			if current == nil {
				current = map[string]any{}
			}
			for key, value := range values {
				current[key] = value
			}
			use.Statuses[status] = current
		}
		declared.Plugins.Values[name] = use
		return nil
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
	descriptionHTML, err := renderMarkdown(result.Description)
	if err != nil {
		writeAPIError(writer, err)
		return
	}
	activity := make([]activityResponse, 0, len(result.Activity))
	for _, entry := range result.Activity {
		bodyHTML := ""
		if entry.Body != "" {
			bodyHTML, err = renderMarkdown(entry.Body)
			if err != nil {
				writeAPIError(writer, err)
				return
			}
		}
		activity = append(activity, activityResponse{ActivityView: entry, BodyHTML: bodyHTML})
	}
	writeJSON(writer, status, taskDetailResponse{Bundle: result, DescriptionHTML: descriptionHTML, Activity: activity})
}

func summariseTask(value *task.Task) (boardTask, error) {
	attachments, err := value.Attachments()
	if err != nil {
		return boardTask{}, err
	}
	return boardTask{
		ID: value.ID, Title: value.Title, Status: value.Status, Project: value.Project,
		Labels: nonNilStrings(value.Labels), Assignee: value.Assignee, Wait: value.Wait,
		References:     nonNilReferences(value.References),
		ActiveSessions: []session.Entry{},
		CreatedAt:      value.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:      value.UpdatedAt.UTC().Format(time.RFC3339Nano),
		ResourceCount:  len(value.References) + len(attachments),
	}, nil
}

func allowJSONMutation(writer http.ResponseWriter, request *http.Request, allowRemoteHost bool) bool {
	if !allowMutationOrigin(writer, request, allowRemoteHost) {
		return false
	}
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeJSON(writer, http.StatusUnsupportedMediaType, map[string]string{"error": "Content-Type must be application/json"})
		return false
	}
	return true
}

func allowMultipartMutation(writer http.ResponseWriter, request *http.Request, allowRemoteHost bool) bool {
	if !allowMutationOrigin(writer, request, allowRemoteHost) {
		return false
	}
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/form-data" {
		writeJSON(writer, http.StatusUnsupportedMediaType, map[string]string{"error": "Content-Type must be multipart/form-data"})
		return false
	}
	return true
}

func allowMutationOrigin(writer http.ResponseWriter, request *http.Request, allowRemoteHost bool) bool {
	if !allowRemoteHost && !isLoopbackRequestHost(request.Host) {
		writeJSON(writer, http.StatusForbidden, map[string]string{"error": "non-loopback Host is not allowed"})
		return false
	}
	origin := request.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	expectedScheme := "http"
	if request.TLS != nil {
		expectedScheme = "https"
	}
	expected, expectedErr := url.Parse(expectedScheme + "://" + request.Host)
	if err != nil || expectedErr != nil || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" ||
		!strings.EqualFold(parsed.Scheme, expected.Scheme) || !strings.EqualFold(parsed.Hostname(), expected.Hostname()) || canonicalPort(parsed) != canonicalPort(expected) {
		writeJSON(writer, http.StatusForbidden, map[string]string{"error": "cross-origin writes are not allowed"})
		return false
	}
	return true
}

func canonicalPort(value *url.URL) string {
	if value.Port() != "" {
		return value.Port()
	}
	if strings.EqualFold(value.Scheme, "https") {
		return "443"
	}
	return "80"
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

func decodeEmptyJSONBody(writer http.ResponseWriter, request *http.Request) bool {
	var input struct{}
	return decodeJSONBody(writer, request, &input)
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
	case strings.Contains(message, "plugin ") && strings.Contains(message, "not installed"):
		status = http.StatusNotFound
	case strings.Contains(message, "already waiting"),
		strings.Contains(message, "does not match active wait"),
		strings.Contains(message, "already has"):
		status = http.StatusConflict
	case strings.Contains(message, "required"),
		strings.Contains(message, "invalid task id"),
		strings.Contains(message, "invalid project id"),
		strings.Contains(message, "invalid wait"),
		strings.Contains(message, "invalid reference"),
		strings.Contains(message, "invalid attachment filename"),
		strings.Contains(message, "not waiting"),
		strings.Contains(message, "cannot be empty"),
		strings.Contains(message, "must be "),
		strings.Contains(message, "is not declared by the plugin"),
		strings.Contains(message, "unknown composed status"),
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

func nonNilReferences(values []task.Reference) []task.Reference {
	if values == nil {
		return []task.Reference{}
	}
	return values
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
