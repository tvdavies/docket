// Package luahook executes one Lua handler inside Docket's isolated hook-runner
// subprocess. The runtime intentionally opens GopherLua's complete standard
// library: process isolation, rather than a restricted language, protects the
// long-lived Docket service from os.exit, blocking IO, and child processes.
package luahook

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tvdavies/docket/internal/actions"
	"github.com/tvdavies/docket/internal/store"
	"github.com/tvdavies/docket/internal/task"
	"github.com/tvdavies/docket/internal/workspace"
	lua "github.com/yuin/gopher-lua"
)

// Options supplies the child runner's explicit process surfaces.
type Options struct {
	Context   context.Context
	Workspace *workspace.Workspace
	Script    string
	Input     io.Reader
	Output    io.Writer
	Error     io.Writer
}

// Run loads a Lua script and calls its global handle(event, docket) function
// once for every JSON event in Input. Any script error fails the complete
// delivery batch so the parent leaves the handler cursor pending for retry.
func Run(options Options) error {
	if options.Workspace == nil {
		return fmt.Errorf("workspace is required")
	}
	if options.Context == nil {
		options.Context = context.Background()
	}
	if options.Input == nil {
		options.Input = strings.NewReader("")
	}
	if options.Output == nil {
		options.Output = os.Stdout
	}
	if options.Error == nil {
		options.Error = os.Stderr
	}

	projectRoot := filepath.Dir(options.Workspace.Root)
	scriptPath := resolvePath(projectRoot, options.Script)

	state := lua.NewState() // Full standard library; this process is disposable.
	defer state.Close()
	state.SetContext(options.Context)

	sdk := newSDK(state, sdkOptions{
		workspace:   options.Workspace,
		projectRoot: projectRoot,
		scriptPath:  scriptPath,
		output:      options.Output,
		errorOutput: options.Error,
		context:     options.Context,
	})
	if err := state.DoFile(scriptPath); err != nil {
		return fmt.Errorf("load %s: %w", options.Script, err)
	}
	handle := state.GetGlobal("handle")
	if handle.Type() != lua.LTFunction {
		return fmt.Errorf("%s must define function handle(event, docket)", options.Script)
	}

	scanner := bufio.NewScanner(options.Input)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal(line, &event); err != nil {
			return fmt.Errorf("decode event: %w", err)
		}
		if err := state.CallByParam(lua.P{Fn: handle, NRet: 0, Protect: true}, toLua(state, event), sdk); err != nil {
			return fmt.Errorf("%s event %v: %w", options.Script, event["seq"], err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read events: %w", err)
	}
	return nil
}

type sdkOptions struct {
	workspace   *workspace.Workspace
	projectRoot string
	scriptPath  string
	output      io.Writer
	errorOutput io.Writer
	context     context.Context
}

func newSDK(state *lua.LState, options sdkOptions) *lua.LTable {
	sdk := state.NewTable()

	state.SetField(sdk, "path", state.NewFunction(func(state *lua.LState) int {
		state.Push(lua.LString(joinLuaPath(state, options.projectRoot)))
		return 1
	}))
	state.SetField(sdk, "asset", state.NewFunction(func(state *lua.LState) int {
		state.Push(lua.LString(joinLuaPath(state, filepath.Dir(options.scriptPath))))
		return 1
	}))

	paths := state.NewTable()
	state.SetField(paths, "project", lua.LString(options.projectRoot))
	state.SetField(paths, "workspace", lua.LString(options.workspace.Root))
	state.SetField(paths, "script", lua.LString(options.scriptPath))
	state.SetField(paths, "script_dir", lua.LString(filepath.Dir(options.scriptPath)))
	state.SetField(sdk, "paths", paths)

	log := state.NewTable()
	state.SetField(log, "info", logFunction(state, options.output, "info"))
	state.SetField(log, "warn", logFunction(state, options.errorOutput, "warn"))
	state.SetField(log, "error", logFunction(state, options.errorOutput, "error"))
	state.SetField(sdk, "log", log)

	fs := state.NewTable()
	state.SetField(fs, "write_atomic", state.NewFunction(func(state *lua.LState) int {
		path := resolvePath(options.projectRoot, state.CheckString(1))
		content := state.CheckString(2)
		permission := os.FileMode(state.OptInt(3, 0o644))
		if err := store.WriteAtomic(path, []byte(content), permission); err != nil {
			state.RaiseError("write %s: %v", path, err)
		}
		return 0
	}))
	state.SetField(sdk, "fs", fs)

	process := state.NewTable()
	state.SetField(process, "run", state.NewFunction(func(state *lua.LState) int {
		program := state.CheckString(1)
		args, err := stringList(state, 2)
		if err != nil {
			state.RaiseError("process.run: %v", err)
			return 0
		}
		command := exec.CommandContext(options.context, program, args...)
		command.Dir = options.projectRoot
		command.Env = os.Environ()
		command.Stdout = options.output
		command.Stderr = options.errorOutput
		if err := command.Run(); err != nil {
			state.RaiseError("process.run %s: %v", program, err)
			return 0
		}
		state.Push(lua.LTrue)
		return 1
	}))
	state.SetField(sdk, "process", process)

	operations := actions.Tasks{
		Workspace: options.workspace,
		Actor:     os.Getenv("DOCKET_ACTOR"),
		Session:   os.Getenv("DOCKET_SESSION"),
	}
	tasks := state.NewTable()
	state.SetField(tasks, "get", state.NewFunction(func(state *lua.LState) int {
		value, err := task.Load(options.workspace, state.CheckString(1))
		if err != nil {
			state.RaiseError("task.get: %v", err)
			return 0
		}
		state.Push(taskValue(state, value))
		return 1
	}))
	state.SetField(tasks, "move", state.NewFunction(func(state *lua.LState) int {
		value, from, err := operations.Move(state.CheckString(1), state.CheckString(2))
		if err != nil {
			state.RaiseError("task.move: %v", err)
			return 0
		}
		state.Push(taskValue(state, value))
		state.Push(lua.LString(from))
		return 2
	}))
	state.SetField(tasks, "assign", state.NewFunction(func(state *lua.LState) int {
		value, err := operations.Assign(state.CheckString(1), state.CheckString(2))
		if err != nil {
			state.RaiseError("task.assign: %v", err)
			return 0
		}
		state.Push(taskValue(state, value))
		return 1
	}))
	state.SetField(tasks, "comment", state.NewFunction(func(state *lua.LState) int {
		comment, err := operations.Comment(state.CheckString(1), state.CheckString(2))
		if err != nil {
			state.RaiseError("task.comment: %v", err)
			return 0
		}
		state.Push(lua.LString(comment.File))
		return 1
	}))
	state.SetField(tasks, "wait", state.NewFunction(func(state *lua.LState) int {
		options := state.CheckTable(2)
		value, err := operations.SetWait(state.CheckString(1), actions.SetWaitOptions{
			Kind:      optionalLuaString(options.RawGetString("kind")),
			Reason:    optionalLuaString(options.RawGetString("reason")),
			Reference: optionalLuaString(options.RawGetString("reference")),
		})
		if err != nil {
			state.RaiseError("task.wait: %v", err)
			return 0
		}
		state.Push(taskValue(state, value))
		return 1
	}))
	state.SetField(tasks, "resume", state.NewFunction(func(state *lua.LState) int {
		value, err := operations.ResolveWait(state.CheckString(1), actions.ResolveWaitOptions{
			WaitID: state.CheckString(2), Result: state.OptString(3, ""),
		})
		if err != nil {
			state.RaiseError("task.resume: %v", err)
			return 0
		}
		state.Push(taskValue(state, value))
		return 1
	}))
	state.SetField(tasks, "reference_add", state.NewFunction(func(state *lua.LState) int {
		value, reference, err := operations.AddReference(
			state.CheckString(1), state.CheckString(2), state.CheckString(3), state.OptString(4, ""),
		)
		if err != nil {
			state.RaiseError("task.reference_add: %v", err)
			return 0
		}
		state.Push(taskValue(state, value))
		state.Push(toLua(state, normaliseValue(reference)))
		return 2
	}))
	state.SetField(tasks, "reference_remove", state.NewFunction(func(state *lua.LState) int {
		value, _, err := operations.RemoveReference(state.CheckString(1), state.CheckString(2))
		if err != nil {
			state.RaiseError("task.reference_remove: %v", err)
			return 0
		}
		state.Push(taskValue(state, value))
		return 1
	}))
	state.SetField(tasks, "label", state.NewFunction(func(state *lua.LState) int {
		add, err := stringList(state, 2)
		if err != nil {
			state.RaiseError("task.label add: %v", err)
			return 0
		}
		remove, err := stringList(state, 3)
		if err != nil {
			state.RaiseError("task.label remove: %v", err)
			return 0
		}
		value, err := operations.Label(state.CheckString(1), add, remove)
		if err != nil {
			state.RaiseError("task.label: %v", err)
			return 0
		}
		state.Push(taskValue(state, value))
		return 1
	}))
	state.SetField(sdk, "task", tasks)

	return sdk
}

func logFunction(state *lua.LState, output io.Writer, level string) *lua.LFunction {
	return state.NewFunction(func(state *lua.LState) int {
		parts := make([]string, 0, state.GetTop())
		for index := 1; index <= state.GetTop(); index++ {
			value := state.Get(index)
			// Permit both docket.log.info("x") and docket.log:info("x").
			if index == 1 && value.Type() == lua.LTTable {
				continue
			}
			parts = append(parts, value.String())
		}
		_, _ = fmt.Fprintf(output, "%s: %s\n", level, strings.Join(parts, " "))
		return 0
	})
}

func joinLuaPath(state *lua.LState, base string) string {
	parts := make([]string, 0, state.GetTop())
	for index := 1; index <= state.GetTop(); index++ {
		parts = append(parts, state.CheckString(index))
	}
	if len(parts) == 0 {
		return base
	}
	if filepath.IsAbs(parts[0]) {
		return filepath.Join(parts...)
	}
	return filepath.Join(append([]string{base}, parts...)...)
}

func resolvePath(base, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(base, path)
}

func optionalLuaString(value lua.LValue) string {
	if value == lua.LNil {
		return ""
	}
	return value.String()
}

func stringList(state *lua.LState, index int) ([]string, error) {
	if state.GetTop() < index || state.Get(index) == lua.LNil {
		return nil, nil
	}
	table, ok := state.Get(index).(*lua.LTable)
	if !ok {
		return nil, fmt.Errorf("argument %d must be an array table", index)
	}
	values := make([]string, table.Len())
	for item := 1; item <= table.Len(); item++ {
		value := table.RawGetInt(item)
		if value.Type() != lua.LTString {
			return nil, fmt.Errorf("argument %d item %d must be a string", index, item)
		}
		values[item-1] = value.String()
	}
	return values, nil
}

func toLua(state *lua.LState, value any) lua.LValue {
	switch typed := value.(type) {
	case nil:
		return lua.LNil
	case bool:
		return lua.LBool(typed)
	case string:
		return lua.LString(typed)
	case float64:
		return lua.LNumber(typed)
	case []any:
		table := state.NewTable()
		for _, item := range typed {
			table.Append(toLua(state, item))
		}
		return table
	case map[string]any:
		table := state.NewTable()
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			state.SetField(table, key, toLua(state, typed[key]))
		}
		return table
	default:
		return lua.LString(fmt.Sprint(typed))
	}
}

func taskValue(state *lua.LState, value *task.Task) *lua.LTable {
	result := state.NewTable()
	fields := map[string]any{
		"id":            value.ID,
		"title":         value.Title,
		"status":        value.Status,
		"project":       value.Project,
		"labels":        value.Labels,
		"assignee":      value.Assignee,
		"wait":          value.Wait,
		"references":    value.References,
		"relationships": value.Relationships,
		"description":   value.Description,
		"created_at":    value.CreatedAt.Format("2006-01-02T15:04:05.999999999Z07:00"),
		"updated_at":    value.UpdatedAt.Format("2006-01-02T15:04:05.999999999Z07:00"),
		"dir":           value.Dir(),
	}
	for key, field := range fields {
		state.SetField(result, key, toLua(state, normaliseValue(field)))
	}
	return result
}

// normaliseValue uses JSON's data model for typed Go slices and maps before
// passing them through the event converter.
func normaliseValue(value any) any {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	var normalised any
	if err := json.Unmarshal(data, &normalised); err != nil {
		return fmt.Sprint(value)
	}
	return normalised
}
