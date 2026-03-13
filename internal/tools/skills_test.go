package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jotavich/xnullclaw/internal/agent"
)

func TestListSkills_Empty(t *testing.T) {
	d, _ := newTestDeps(t)
	r := NewRegistry()
	registerSkillTools(r, d)

	// No agent specified — lists shared skills (which are empty).
	result, err := r.Execute(context.Background(), "list_skills", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "No skills") {
		t.Errorf("expected 'No skills' message, got %q", result)
	}
}

func TestListSkills_NonexistentAgent(t *testing.T) {
	d, _ := newTestDeps(t)
	r := NewRegistry()
	registerSkillTools(r, d)

	_, err := r.Execute(context.Background(), "list_skills", map[string]any{
		"agent": "nonexistent",
	})
	if err == nil {
		t.Fatal("expected error for nonexistent agent")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("expected 'does not exist' in error, got: %v", err)
	}
}

func TestInstallSkill_SharedHappy(t *testing.T) {
	d, _ := newTestDeps(t)

	// Create a skill source directory inside the xnc home.
	skillDir := filepath.Join(d.Home, "test-skill")
	os.MkdirAll(skillDir, 0755)
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# Test Skill\nA test skill."), 0644)

	r := NewRegistry()
	registerSkillTools(r, d)

	result, err := r.Execute(context.Background(), "install_skill", map[string]any{
		"source":          skillDir,
		"sync_to_agents":  false,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Installed skill") {
		t.Errorf("expected 'Installed skill' in result, got %q", result)
	}
	if !strings.Contains(result, "shared") {
		t.Errorf("expected 'shared' in result, got %q", result)
	}
}

func TestInstallSkill_AgentHappy(t *testing.T) {
	d, _ := newTestDeps(t)
	agent.Setup(d.Home, "Alice", agent.SetupOpts{})

	// Create a skill source directory inside the xnc home.
	skillDir := filepath.Join(d.Home, "test-skill")
	os.MkdirAll(skillDir, 0755)
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# Test Skill\nA test skill."), 0644)

	r := NewRegistry()
	registerSkillTools(r, d)

	result, err := r.Execute(context.Background(), "install_skill", map[string]any{
		"source": skillDir,
		"agent":  "Alice",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Installed skill") {
		t.Errorf("expected 'Installed skill' in result, got %q", result)
	}
	if !strings.Contains(result, "Alice") {
		t.Errorf("expected 'Alice' in result, got %q", result)
	}
}

func TestInstallSkill_SourceOutsideHome(t *testing.T) {
	d, _ := newTestDeps(t)
	r := NewRegistry()
	registerSkillTools(r, d)

	// Source path outside of xnc home should be rejected.
	_, err := r.Execute(context.Background(), "install_skill", map[string]any{
		"source": "/tmp/evil-skill",
	})
	if err == nil {
		t.Fatal("expected error for source outside home")
	}
	// Error could be about path not existing or not being within home.
}

func TestRemoveSkill_AgentHappy(t *testing.T) {
	d, _ := newTestDeps(t)
	agent.Setup(d.Home, "Alice", agent.SetupOpts{})

	// Install a skill first.
	skillDir := filepath.Join(d.Home, "rm-skill")
	os.MkdirAll(skillDir, 0755)
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# RM Skill\nA removable skill."), 0644)

	r := NewRegistry()
	registerSkillTools(r, d)

	r.Execute(context.Background(), "install_skill", map[string]any{
		"source": skillDir,
		"agent":  "Alice",
	})

	// Now remove it.
	result, err := r.Execute(context.Background(), "remove_skill", map[string]any{
		"name":  "rm-skill",
		"agent": "Alice",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Removed skill") {
		t.Errorf("expected 'Removed skill' in result, got %q", result)
	}
}

func TestRemoveSkill_NonexistentAgent(t *testing.T) {
	d, _ := newTestDeps(t)
	r := NewRegistry()
	registerSkillTools(r, d)

	_, err := r.Execute(context.Background(), "remove_skill", map[string]any{
		"name":  "whatever",
		"agent": "nonexistent",
	})
	if err == nil {
		t.Fatal("expected error for nonexistent agent")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("expected 'does not exist' in error, got: %v", err)
	}
}
