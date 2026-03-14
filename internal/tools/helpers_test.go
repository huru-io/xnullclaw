package tools

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jotavich/xnullclaw/internal/agent"
	"github.com/jotavich/xnullclaw/internal/config"
	"github.com/jotavich/xnullclaw/internal/docker"
)

func newTestDeps(t *testing.T) (Deps, *docker.MockOps) {
	t.Helper()
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, "mux"), 0755)
	store := newTestStore(t)
	cfg := config.DefaultConfig()
	mock := &docker.MockOps{}
	cfgPath := filepath.Join(home, "mux", "config.json")
	backend := &agent.LocalBackend{Home: home}
	return Deps{Docker: mock, Backend: backend, Store: store, Cfg: cfg, CfgPath: cfgPath, Home: home, Image: "test:latest"}, mock
}
