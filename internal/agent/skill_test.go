package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSanitizeSkillName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"My Skill", "my-skill"},
		{"coding standards", "coding-standards"},
		{"  spaces  ", "spaces"},
		{"UPPER_CASE", "upper_case"},
		{"special!@#$chars", "specialchars"},
		{"multiple---hyphens", "multiple-hyphens"},
		{"_leading_trailing_", "leading_trailing"},
		{"", ""},
		{"a", "a"},
		{"日本語", ""},
	}
	for _, tt := range tests {
		got := sanitizeSkillName(tt.input)
		if got != tt.want {
			t.Errorf("sanitizeSkillName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestSanitizeSkillNameTruncation(t *testing.T) {
	long := "abcdefghijklmnopqrstuvwxyz-abcdefghijklmnopqrstuvwxyz"
	got := sanitizeSkillName(long)
	if len(got) > 40 {
		t.Errorf("sanitizeSkillName: expected len <= 40, got %d", len(got))
	}
}

func TestParseSkillNameFromMD(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"standard heading", "# My Cool Skill\n\nContent here.", "my-cool-skill"},
		{"heading with spaces", "#  Spaced  Out  \nMore.", "spaced-out"},
		{"no heading", "No heading here.\nJust text.", ""},
		{"h2 not h1", "## Subheading\nContent.", ""},
		{"empty heading", "# \nContent.", ""},
		{"unicode heading", "# 日本語\nContent.", ""},
		{"mixed heading", "# Code Review 2024\nContent.", "code-review-2024"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseSkillNameFromMD([]byte(tt.input))
			if got != tt.want {
				t.Errorf("parseSkillNameFromMD = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseTOMLField(t *testing.T) {
	toml := `[skill]
name = "my-skill"
version = "1.0.0"
version_extra = "beta"
description = "A test skill"
author = "Alice"
authoritative = "yes"
commented = "value" # inline comment
single_quoted = 'hello'
`
	tests := []struct {
		key  string
		want string
	}{
		{"name", "my-skill"},
		{"version", "1.0.0"},
		{"version_extra", "beta"},
		{"description", "A test skill"},
		{"author", "Alice"},
		{"authoritative", "yes"},
		{"commented", "value"},
		{"single_quoted", "hello"},
		{"nonexistent", ""},
	}
	for _, tt := range tests {
		got := parseTOMLField(toml, tt.key)
		if got != tt.want {
			t.Errorf("parseTOMLField(%q) = %q, want %q", tt.key, got, tt.want)
		}
	}
}

func TestValidateSkillName(t *testing.T) {
	// Valid names.
	for _, name := range []string{"my-skill", "skill123", "test_skill"} {
		if err := validateSkillName(name); err != nil {
			t.Errorf("validateSkillName(%q) unexpected error: %v", name, err)
		}
	}
	// Invalid names.
	for _, name := range []string{"", "../escape", ".hidden", "path/traversal", "back\\slash"} {
		if err := validateSkillName(name); err == nil {
			t.Errorf("validateSkillName(%q) expected error, got nil", name)
		}
	}
}

func TestSkillConflicts(t *testing.T) {
	dir := t.TempDir()

	// Create "my-skill" directory.
	os.MkdirAll(filepath.Join(dir, "my-skill"), 0755)
	os.WriteFile(filepath.Join(dir, "my-skill", "SKILL.md"), []byte("# test"), 0644)

	// "my_skill" should conflict (same canonical name).
	conflict, found := skillConflicts(dir, "my_skill")
	if !found {
		t.Error("expected conflict for my_skill, got none")
	}
	if conflict != "my-skill" {
		t.Errorf("expected conflict with my-skill, got %q", conflict)
	}

	// "my-skill" should not conflict (exact match = update).
	_, found = skillConflicts(dir, "my-skill")
	if found {
		t.Error("exact match should not be a conflict")
	}

	// "other-skill" should not conflict.
	_, found = skillConflicts(dir, "other-skill")
	if found {
		t.Error("different name should not conflict")
	}
}

func TestInstallFromDir(t *testing.T) {
	src := t.TempDir()
	target := t.TempDir()

	// Create skill source.
	os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("# Test Skill\nContent."), 0644)
	os.WriteFile(filepath.Join(src, "SKILL.toml"), []byte(`[skill]
name = "test"
version = "1.0"
`), 0644)

	// Create a named subdirectory to use as source.
	skillSrc := filepath.Join(src, "test-skill")
	os.MkdirAll(skillSrc, 0755)
	os.WriteFile(filepath.Join(skillSrc, "SKILL.md"), []byte("# Test Skill\nContent."), 0644)

	name, err := InstallSkill(target, skillSrc)
	if err != nil {
		t.Fatalf("InstallSkill: %v", err)
	}
	if name != "test-skill" {
		t.Errorf("expected name test-skill, got %q", name)
	}

	// Verify files exist.
	if _, err := os.Stat(filepath.Join(target, "test-skill", "SKILL.md")); err != nil {
		t.Error("SKILL.md not found in installed skill")
	}
}

func TestInstallFromMD(t *testing.T) {
	target := t.TempDir()
	src := filepath.Join(t.TempDir(), "review-checklist.md")
	os.WriteFile(src, []byte("# Code Review Checklist\n\n- Check tests\n- Check types"), 0644)

	name, err := InstallSkill(target, src)
	if err != nil {
		t.Fatalf("InstallSkill from MD: %v", err)
	}
	if name != "code-review-checklist" {
		t.Errorf("expected code-review-checklist, got %q", name)
	}

	// Verify SKILL.md exists.
	data, err := os.ReadFile(filepath.Join(target, "code-review-checklist", "SKILL.md"))
	if err != nil {
		t.Fatal("SKILL.md not found")
	}
	if len(data) == 0 {
		t.Error("SKILL.md is empty")
	}
}

func TestListSkills(t *testing.T) {
	dir := t.TempDir()

	// Empty dir.
	skills, err := ListSkills(dir)
	if err != nil {
		t.Fatalf("ListSkills on empty: %v", err)
	}
	if len(skills) != 0 {
		t.Errorf("expected 0 skills, got %d", len(skills))
	}

	// Non-existent dir.
	skills, err = ListSkills(filepath.Join(dir, "nope"))
	if err != nil {
		t.Fatalf("ListSkills on non-existent: %v", err)
	}
	if skills != nil {
		t.Error("expected nil for non-existent dir")
	}

	// Add a skill.
	os.MkdirAll(filepath.Join(dir, "my-skill"), 0755)
	os.WriteFile(filepath.Join(dir, "my-skill", "SKILL.md"), []byte("# My Skill"), 0644)

	// Add a non-skill dir (should be ignored).
	os.MkdirAll(filepath.Join(dir, "not-a-skill"), 0755)

	// Add a hidden dir (should be ignored).
	os.MkdirAll(filepath.Join(dir, ".hidden"), 0755)
	os.WriteFile(filepath.Join(dir, ".hidden", "SKILL.md"), []byte("# Hidden"), 0644)

	skills, err = ListSkills(dir)
	if err != nil {
		t.Fatalf("ListSkills: %v", err)
	}
	if len(skills) != 1 {
		t.Errorf("expected 1 skill, got %d", len(skills))
	}
	if skills[0].Name != "my-skill" {
		t.Errorf("expected my-skill, got %q", skills[0].Name)
	}
}

func TestRemoveSkill(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "to-remove"), 0755)
	os.WriteFile(filepath.Join(dir, "to-remove", "SKILL.md"), []byte("test"), 0644)

	if err := RemoveSkill(dir, "to-remove"); err != nil {
		t.Fatalf("RemoveSkill: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "to-remove")); !os.IsNotExist(err) {
		t.Error("skill directory still exists after removal")
	}

	// Remove non-existent.
	if err := RemoveSkill(dir, "nope"); err == nil {
		t.Error("expected error for non-existent skill")
	}

	// Path traversal.
	if err := RemoveSkill(dir, "../escape"); err == nil {
		t.Error("expected error for path traversal skill name")
	}
}

func TestRemoveSkillPathTraversal(t *testing.T) {
	dir := t.TempDir()
	outer := filepath.Join(dir, "outer")
	skills := filepath.Join(dir, "skills")
	os.MkdirAll(outer, 0755)
	os.MkdirAll(skills, 0755)

	// Try to remove "../outer" from skills/ — should be rejected.
	err := RemoveSkill(skills, "../outer")
	if err == nil {
		t.Error("expected error for path traversal")
	}
	// Verify outer still exists.
	if _, err := os.Stat(outer); err != nil {
		t.Error("outer directory was deleted by path traversal")
	}
}

func TestEnforceBoundary(t *testing.T) {
	if err := enforceBoundary("/a/b/c", "/a/b"); err != nil {
		t.Errorf("should allow /a/b/c under /a/b: %v", err)
	}
	if err := enforceBoundary("/a/b/../c", "/a/b"); err == nil {
		t.Error("should reject /a/b/../c under /a/b")
	}
	if err := enforceBoundary("/a/b", "/a/b"); err != nil {
		t.Errorf("should allow /a/b == /a/b: %v", err)
	}
}
