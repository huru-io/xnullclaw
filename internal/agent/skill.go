package agent

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Skill size limits.
const (
	maxSkillFileSize  = 10 * 1024 * 1024  // 10 MB per file
	maxSkillTotalSize = 50 * 1024 * 1024  // 50 MB total extraction
	maxSkillZipFiles  = 500               // max files in a zip
	maxSkillMDSize    = 5 * 1024 * 1024   // 5 MB for a single .md file
)

// SkillInfo holds metadata about an installed skill.
type SkillInfo struct {
	Name        string `json:"name"`
	Version     string `json:"version,omitempty"`
	Description string `json:"description,omitempty"`
	Author      string `json:"author,omitempty"`
	Path        string `json:"path"`
	HasTOML     bool   `json:"has_toml"`
	HasJSON     bool   `json:"has_json"`
	HasMD       bool   `json:"has_md"`
}

// SharedSkillsDir returns the path to the shared skills repository.
func SharedSkillsDir(home string) string {
	return filepath.Join(home, "skills")
}

// AgentSkillsDir returns the path to an agent's workspace skills.
func AgentSkillsDir(home, name string) string {
	return filepath.Join(Dir(home, name), "data", "workspace", "skills")
}

// validateSkillName rejects names with path traversal or special characters.
func validateSkillName(name string) error {
	if name == "" {
		return fmt.Errorf("skill name is empty")
	}
	if strings.ContainsAny(name, "/\\") || strings.HasPrefix(name, ".") {
		return fmt.Errorf("invalid skill name %q", name)
	}
	return nil
}

// enforceBoundary ensures path is under the given root directory.
func enforceBoundary(path, root string) error {
	cleanPath := filepath.Clean(path)
	cleanRoot := filepath.Clean(root)
	if !strings.HasPrefix(cleanPath+"/", cleanRoot+"/") && cleanPath != cleanRoot {
		return fmt.Errorf("path %q escapes boundary %q", path, root)
	}
	return nil
}

// skillConflicts checks if a skill name conflicts with any existing skill
// in the target directory (same canonical name, different directory name).
func skillConflicts(targetDir, name string) (string, bool) {
	canon := CanonicalName(name) // reuse agent canonical name logic
	entries, err := os.ReadDir(targetDir)
	if err != nil {
		return "", false
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if e.Name() == name {
			continue // exact match is an update, not a conflict
		}
		if CanonicalName(e.Name()) == canon {
			return e.Name(), true
		}
	}
	return "", false
}

// sanitizeSkillName cleans a raw name into a valid skill directory name.
var skillNameCleanRe = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

func sanitizeSkillName(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, " ", "-")
	s = skillNameCleanRe.ReplaceAllString(s, "")
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	s = strings.Trim(s, "-_")
	if len(s) > 40 {
		s = s[:40]
	}
	return s
}

// parseSkillNameFromMD extracts a skill name from a SKILL.md file.
// Looks for the first `# Heading` line and sanitizes it into a directory name.
func parseSkillNameFromMD(data []byte) string {
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			heading := strings.TrimPrefix(line, "# ")
			heading = strings.TrimSpace(heading)
			if heading != "" {
				return sanitizeSkillName(heading)
			}
		}
	}
	return ""
}

// ListSkills returns skills found in the given directory.
func ListSkills(skillsDir string) ([]SkillInfo, error) {
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var skills []SkillInfo
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		info := readSkillInfo(filepath.Join(skillsDir, e.Name()))
		if info.HasTOML || info.HasJSON || info.HasMD {
			skills = append(skills, info)
		}
	}
	return skills, nil
}

// readSkillInfo reads basic metadata from a skill directory.
func readSkillInfo(dir string) SkillInfo {
	name := filepath.Base(dir)
	info := SkillInfo{
		Name: name,
		Path: dir,
	}

	// Try reading TOML directly (no double stat).
	if data, err := os.ReadFile(filepath.Join(dir, "SKILL.toml")); err == nil {
		info.HasTOML = true
		info.Version = parseTOMLField(string(data), "version")
		info.Description = parseTOMLField(string(data), "description")
		info.Author = parseTOMLField(string(data), "author")
	}
	if _, err := os.Stat(filepath.Join(dir, "skill.json")); err == nil {
		info.HasJSON = true
	}
	if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err == nil {
		info.HasMD = true
	}
	return info
}

// parseTOMLField does a simple line scan for `key = "value"`.
// Requires exact key match (not prefix) and strips inline comments.
func parseTOMLField(data, key string) string {
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		if strings.TrimSpace(parts[0]) != key {
			continue
		}
		v := strings.TrimSpace(parts[1])
		// Strip surrounding quotes and inline comments.
		if len(v) >= 2 && v[0] == '"' {
			// Find closing quote.
			end := strings.Index(v[1:], "\"")
			if end >= 0 {
				return v[1 : end+1]
			}
		}
		if len(v) >= 2 && v[0] == '\'' {
			end := strings.Index(v[1:], "'")
			if end >= 0 {
				return v[1 : end+1]
			}
		}
		// Unquoted value — strip inline comment.
		if idx := strings.Index(v, "#"); idx > 0 {
			v = strings.TrimSpace(v[:idx])
		}
		return v
	}
	return ""
}

// InstallSkill installs a skill from a source path to the target skills directory.
// Source can be a directory, a .zip file, or a single .md file.
func InstallSkill(targetDir, source string) (string, error) {
	fi, err := os.Stat(source)
	if err != nil {
		return "", fmt.Errorf("source not found: %w", err)
	}

	if fi.IsDir() {
		return installFromDir(targetDir, source)
	}

	ext := strings.ToLower(filepath.Ext(source))
	switch ext {
	case ".zip":
		return installFromZip(targetDir, source)
	case ".md":
		return installFromMD(targetDir, source)
	default:
		return "", fmt.Errorf("unsupported source type: %s (use directory, .zip, or .md)", ext)
	}
}

// checkSkillConflict returns an error if the skill name conflicts with existing skills.
func checkSkillConflict(targetDir, name string) error {
	if conflict, found := skillConflicts(targetDir, name); found {
		return fmt.Errorf("skill %q conflicts with existing skill %q (canonical names match)", name, conflict)
	}
	return nil
}

// installFromDir copies a skill directory to the target.
func installFromDir(targetDir, source string) (string, error) {
	name := filepath.Base(source)

	if err := validateSkillName(name); err != nil {
		return "", err
	}
	if !hasSkillFiles(source) {
		return "", fmt.Errorf("directory %s does not contain SKILL.toml, skill.json, or SKILL.md", source)
	}
	if err := checkSkillConflict(targetDir, name); err != nil {
		return "", err
	}

	dest := filepath.Join(targetDir, name)
	if err := enforceBoundary(dest, targetDir); err != nil {
		return "", err
	}
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return "", fmt.Errorf("create target dir: %w", err)
	}

	os.RemoveAll(dest)

	if err := copyDir(source, dest); err != nil {
		return "", fmt.Errorf("copy skill: %w", err)
	}
	return name, nil
}

// installFromZip extracts a zip archive to the target skills directory.
func installFromZip(targetDir, source string) (string, error) {
	r, err := zip.OpenReader(source)
	if err != nil {
		return "", fmt.Errorf("open zip: %w", err)
	}
	defer r.Close()

	// Enforce file count limit.
	if len(r.File) > maxSkillZipFiles {
		return "", fmt.Errorf("zip has too many files (%d > %d)", len(r.File), maxSkillZipFiles)
	}

	// Detect skill name from zip structure.
	skillName := sanitizeSkillName(strings.TrimSuffix(filepath.Base(source), ".zip"))
	hasTopDir := true
	topDir := ""

	for _, f := range r.File {
		parts := strings.SplitN(f.Name, "/", 2)
		if topDir == "" {
			topDir = parts[0]
		} else if parts[0] != topDir {
			hasTopDir = false
			break
		}
	}
	// Use top-level directory name if it looks like a directory (not a bare file).
	if hasTopDir && topDir != "" && !strings.Contains(topDir, ".") {
		skillName = sanitizeSkillName(topDir)
	}

	if skillName == "" {
		return "", fmt.Errorf("could not determine skill name from zip")
	}
	if err := validateSkillName(skillName); err != nil {
		return "", err
	}
	if err := checkSkillConflict(targetDir, skillName); err != nil {
		return "", err
	}

	dest := filepath.Join(targetDir, skillName)
	if err := enforceBoundary(dest, targetDir); err != nil {
		return "", err
	}
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return "", fmt.Errorf("create target dir: %w", err)
	}
	os.RemoveAll(dest)

	var totalWritten int64
	for _, f := range r.File {
		relPath := f.Name
		if hasTopDir && topDir != "" && !strings.Contains(topDir, ".") {
			relPath = strings.TrimPrefix(relPath, topDir+"/")
			if relPath == "" {
				continue
			}
		}

		outPath := filepath.Join(dest, relPath)

		// Sanitize: no path traversal.
		if err := enforceBoundary(outPath, dest); err != nil {
			continue
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(outPath, 0755); err != nil {
				os.RemoveAll(dest)
				return "", fmt.Errorf("create dir %s: %w", relPath, err)
			}
			continue
		}

		// Skip symlinks in zip (mode check).
		if f.Mode()&os.ModeSymlink != 0 {
			continue
		}

		if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
			os.RemoveAll(dest)
			return "", fmt.Errorf("create parent dir for %s: %w", relPath, err)
		}
		n, err := extractZipFile(f, outPath)
		if err != nil {
			os.RemoveAll(dest)
			return "", fmt.Errorf("extract %s: %w", f.Name, err)
		}
		totalWritten += n
		if totalWritten > maxSkillTotalSize {
			os.RemoveAll(dest)
			return "", fmt.Errorf("zip extraction exceeds size limit (%d MB)", maxSkillTotalSize/(1024*1024))
		}
	}

	if !hasSkillFiles(dest) {
		os.RemoveAll(dest)
		return "", fmt.Errorf("zip does not contain SKILL.toml, skill.json, or SKILL.md")
	}

	return skillName, nil
}

// installFromMD creates a minimal skill from a single .md file.
// Name resolution order:
//  1. First `# Heading` inside the markdown (sanitized)
//  2. Filename (e.g. "coding-standards.md" → "coding-standards")
//  3. Parent directory name (when file is literally "SKILL.md")
func installFromMD(targetDir, source string) (string, error) {
	// Check file size before reading into memory.
	fi, err := os.Stat(source)
	if err != nil {
		return "", fmt.Errorf("stat source: %w", err)
	}
	if fi.Size() > maxSkillMDSize {
		return "", fmt.Errorf("skill .md file too large (%d bytes, max %d)", fi.Size(), maxSkillMDSize)
	}

	data, err := os.ReadFile(source)
	if err != nil {
		return "", fmt.Errorf("read source: %w", err)
	}

	// Try to extract name from first heading.
	name := parseSkillNameFromMD(data)

	// Fall back to filename.
	if name == "" {
		base := filepath.Base(source)
		name = strings.TrimSuffix(base, filepath.Ext(base))
		if strings.EqualFold(name, "SKILL") || strings.EqualFold(name, "skill") {
			name = filepath.Base(filepath.Dir(source))
			if name == "." || name == "/" {
				return "", fmt.Errorf("cannot determine skill name from SKILL.md — rename the file or add a # Heading")
			}
		}
		name = sanitizeSkillName(name)
	}

	if name == "" {
		return "", fmt.Errorf("cannot determine skill name — add a # Heading to the markdown")
	}
	if err := validateSkillName(name); err != nil {
		return "", err
	}
	if err := checkSkillConflict(targetDir, name); err != nil {
		return "", err
	}

	dest := filepath.Join(targetDir, name)
	if err := enforceBoundary(dest, targetDir); err != nil {
		return "", err
	}
	if err := os.MkdirAll(dest, 0755); err != nil {
		return "", fmt.Errorf("create skill dir: %w", err)
	}

	if err := os.WriteFile(filepath.Join(dest, "SKILL.md"), data, 0644); err != nil {
		return "", fmt.Errorf("write SKILL.md: %w", err)
	}

	return name, nil
}

// hasSkillFiles checks if a directory contains at least one skill manifest.
func hasSkillFiles(dir string) bool {
	for _, f := range []string{"SKILL.toml", "skill.json", "SKILL.md"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			return true
		}
	}
	return false
}

// RemoveSkill removes a skill from the given directory.
func RemoveSkill(skillsDir, name string) error {
	if err := validateSkillName(name); err != nil {
		return err
	}
	p := filepath.Join(skillsDir, name)
	if err := enforceBoundary(p, skillsDir); err != nil {
		return err
	}
	if _, err := os.Stat(p); os.IsNotExist(err) {
		return fmt.Errorf("skill %q not found in %s", name, skillsDir)
	}
	return os.RemoveAll(p)
}

// SyncSharedToAgents copies a shared skill to all existing agents' workspace skills.
func SyncSharedToAgents(home, skillName string) (int, error) {
	if err := validateSkillName(skillName); err != nil {
		return 0, err
	}
	source := filepath.Join(SharedSkillsDir(home), skillName)
	if _, err := os.Stat(source); os.IsNotExist(err) {
		return 0, fmt.Errorf("shared skill %q not found", skillName)
	}

	agents, err := ListAll(home)
	if err != nil {
		return 0, err
	}

	count := 0
	for _, a := range agents {
		agentSkillsDir := AgentSkillsDir(home, a.Name)
		dest := filepath.Join(agentSkillsDir, skillName)
		if err := os.MkdirAll(agentSkillsDir, 0755); err != nil {
			return count, fmt.Errorf("create skills dir for %s: %w", a.Name, err)
		}
		// Copy to temp first, then rename for atomicity.
		tmp := dest + ".tmp"
		os.RemoveAll(tmp)
		if err := copyDir(source, tmp); err != nil {
			os.RemoveAll(tmp)
			return count, fmt.Errorf("copy to %s: %w", a.Name, err)
		}
		os.RemoveAll(dest)
		if err := os.Rename(tmp, dest); err != nil {
			os.RemoveAll(tmp)
			return count, fmt.Errorf("rename temp for %s: %w", a.Name, err)
		}
		count++
	}
	return count, nil
}

// RemoveSkillFromAllAgents removes a skill from all agents' workspace skills.
func RemoveSkillFromAllAgents(home, skillName string) (int, error) {
	if err := validateSkillName(skillName); err != nil {
		return 0, err
	}

	agents, err := ListAll(home)
	if err != nil {
		return 0, err
	}

	count := 0
	for _, a := range agents {
		p := filepath.Join(AgentSkillsDir(home, a.Name), skillName)
		if _, err := os.Stat(p); err == nil {
			if err := os.RemoveAll(p); err == nil {
				count++
			}
		}
	}
	return count, nil
}

// InstallSharedToAgent copies all shared skills into an agent's workspace.
// Skips skills that already exist in the agent's workspace (agent overrides shared).
func InstallSharedToAgent(home, agentName string) error {
	shared := SharedSkillsDir(home)
	skills, err := ListSkills(shared)
	if err != nil || len(skills) == 0 {
		return nil
	}

	target := AgentSkillsDir(home, agentName)
	for _, s := range skills {
		dest := filepath.Join(target, s.Name)
		if _, err := os.Stat(dest); err == nil {
			continue
		}
		if err := os.MkdirAll(target, 0755); err != nil {
			return err
		}
		if err := copyDir(s.Path, dest); err != nil {
			return fmt.Errorf("copy shared skill %s to %s: %w", s.Name, agentName, err)
		}
	}
	return nil
}

// extractZipFile extracts a single file from a zip archive.
// Returns the number of bytes written or an error if the file exceeds the size limit.
func extractZipFile(f *zip.File, destPath string) (int64, error) {
	// Reject files that declare a size over the limit.
	if f.UncompressedSize64 > uint64(maxSkillFileSize) {
		return 0, fmt.Errorf("file %s too large (%d bytes, max %d)", f.Name, f.UncompressedSize64, maxSkillFileSize)
	}

	rc, err := f.Open()
	if err != nil {
		return 0, err
	}
	defer rc.Close()

	out, err := os.Create(destPath)
	if err != nil {
		return 0, err
	}
	defer out.Close()

	n, err := io.Copy(out, io.LimitReader(rc, maxSkillFileSize+1))
	if err != nil {
		return n, err
	}
	if n > maxSkillFileSize {
		os.Remove(destPath)
		return 0, fmt.Errorf("file %s exceeds size limit (%d bytes)", f.Name, maxSkillFileSize)
	}
	return n, nil
}
