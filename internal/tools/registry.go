// Package tools defines the tool registry and implementations for the mux loop.
// All tools call Go functions directly via docker.Ops and agent packages —
// no subprocess spawning.
package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jotavich/xnullclaw/internal/config"
	"github.com/jotavich/xnullclaw/internal/docker"
	"github.com/jotavich/xnullclaw/internal/memory"
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

// Deps holds all the dependencies that tools need.
type Deps struct {
	Docker     docker.Ops
	Store      *memory.Store
	Cfg        *config.Config
	CfgPath    string
	Home       string // XNC home directory
	Image      string // Docker image name
}

// RegisterAll registers all tool sets into the registry.
func RegisterAll(r *Registry, d Deps) {
	registerAgentTools(r, d)
	registerFileTools(r, d)
	registerMemoryTools(r, d.Store)
	registerPersonaTools(r, d)
	registerCostTools(r, d)
	registerPassthroughTools(r, d.Store)
	registerSkillTools(r, d)
	registerSchedulerTools(r, d.Store)
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
