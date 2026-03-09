// Package tools defines the tool registry and tool-calling interface for the mux loop.
package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/jotavich/xnullclaw/mux/config"
	"github.com/jotavich/xnullclaw/mux/memory"
)

// Definition is an OpenAI function tool definition.
type Definition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// Executor runs a tool and returns the result as a string.
type Executor func(ctx context.Context, args map[string]any) (string, error)

// Registry holds all registered tools.
type Registry struct {
	defs      []Definition
	executors map[string]Executor
}

// NewRegistry creates an empty tool registry.
func NewRegistry() *Registry {
	return &Registry{
		executors: make(map[string]Executor),
	}
}

// Register adds a tool definition and its executor to the registry.
func (r *Registry) Register(def Definition, exec Executor) {
	r.defs = append(r.defs, def)
	r.executors[def.Name] = exec
}

// Definitions returns all registered tool definitions.
func (r *Registry) Definitions() []Definition {
	out := make([]Definition, len(r.defs))
	copy(out, r.defs)
	return out
}

// Execute runs the named tool with the given arguments.
func (r *Registry) Execute(ctx context.Context, name string, args map[string]any) (string, error) {
	exec, ok := r.executors[name]
	if !ok {
		return "", fmt.Errorf("unknown tool: %s", name)
	}
	return exec(ctx, args)
}

// RegisterAll registers all tool sets into the registry.
func RegisterAll(r *Registry, cfg *config.Config, configPath string, store *memory.Store, wrapperPath string) {
	registerAgentTools(r, wrapperPath)
	registerFileTools(r, wrapperPath)
	registerMemoryTools(r, store)
	registerPersonaTools(r, cfg, configPath)
	registerCostTools(r, cfg, configPath, store, wrapperPath)
	registerPassthroughTools(r, store)
}

// ---------------------------------------------------------------------------
// Helpers for shelling out to xnullclaw
// ---------------------------------------------------------------------------

// RunWrapper executes an xnullclaw command and returns stdout.
// Exported for use by main.go during startup/shutdown.
func RunWrapper(ctx context.Context, wrapperPath string, args ...string) (string, error) {
	return runWrapper(ctx, wrapperPath, args...)
}

// runWrapper executes an xnullclaw command and returns stdout.
func runWrapper(ctx context.Context, wrapperPath string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, wrapperPath, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s: %s", err, stderr.String())
	}
	return stdout.String(), nil
}

// runWrapperWithStdin executes with stdin input.
func runWrapperWithStdin(ctx context.Context, wrapperPath string, stdin string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, wrapperPath, args...)
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s: %s", err, stderr.String())
	}
	return stdout.String(), nil
}

// ---------------------------------------------------------------------------
// Arg extraction helpers
// ---------------------------------------------------------------------------

func stringArg(args map[string]any, key string) (string, error) {
	v, ok := args[key]
	if !ok {
		return "", fmt.Errorf("missing required argument: %s", key)
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("argument %s must be a string", key)
	}
	return s, nil
}

func float64Arg(args map[string]any, key string) (float64, error) {
	v, ok := args[key]
	if !ok {
		return 0, fmt.Errorf("missing required argument: %s", key)
	}
	switch n := v.(type) {
	case float64:
		return n, nil
	case int:
		return float64(n), nil
	case json.Number:
		return n.Float64()
	default:
		return 0, fmt.Errorf("argument %s must be a number", key)
	}
}

func stringSliceArg(args map[string]any, key string) ([]string, error) {
	v, ok := args[key]
	if !ok {
		return nil, fmt.Errorf("missing required argument: %s", key)
	}
	switch arr := v.(type) {
	case []any:
		out := make([]string, 0, len(arr))
		for _, elem := range arr {
			s, ok := elem.(string)
			if !ok {
				return nil, fmt.Errorf("argument %s must be an array of strings", key)
			}
			out = append(out, s)
		}
		return out, nil
	case []string:
		return arr, nil
	default:
		return nil, fmt.Errorf("argument %s must be an array of strings", key)
	}
}

func optionalStringArg(args map[string]any, key string, fallback string) string {
	v, ok := args[key]
	if !ok {
		return fallback
	}
	s, ok := v.(string)
	if !ok {
		return fallback
	}
	return s
}
