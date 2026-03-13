package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Rename changes an agent's name. The agent must be stopped first.
// Steps: validate new name, rename directory, update .meta, update system_prompt,
// write identity-change note for the agent to discover.
func Rename(home, oldName, newName string) error {
	if err := ValidateName(newName); err != nil {
		return err
	}
	if !Exists(home, oldName) {
		return fmt.Errorf("agent %q does not exist", oldName)
	}

	oldCanon := CanonicalName(oldName)
	newCanon := CanonicalName(newName)

	if oldCanon == newCanon {
		// Same canonical name — just update display name in .meta.
		dir := Dir(home, oldName)
		return WriteMeta(dir, "NAME", newName)
	}

	if Exists(home, newName) {
		return fmt.Errorf("agent %q already exists", newName)
	}
	if conflict, found := ConflictsWith(home, newName); found {
		return fmt.Errorf("agent %q conflicts with existing agent %q", newName, conflict)
	}

	oldDir := Dir(home, oldName)
	newDir := Dir(home, newName)

	// Rename directory.
	if err := os.Rename(oldDir, newDir); err != nil {
		return fmt.Errorf("rename directory: %w", err)
	}

	// Update .meta display name.
	if err := WriteMeta(newDir, "NAME", newName); err != nil {
		return fmt.Errorf("update meta: %w", err)
	}

	// Update system_prompt — replace old name with new name.
	updateSystemPromptName(newDir, oldName, newName)

	// Write identity-change note so the agent knows on next interaction.
	writeIdentityChange(newDir, oldName, newName)

	return nil
}

// IdentityChangeMessage returns the message to send to an agent after rename.
func IdentityChangeMessage(oldName, newName string) string {
	return fmt.Sprintf(
		"IDENTITY UPDATE: You have been renamed from %s to %s. "+
			"Your name is now %s. Update any references to your old name. "+
			"Acknowledge briefly.",
		oldName, newName, newName,
	)
}

// writeIdentityChange updates workspace files to reflect the new name:
// replaces old name in IDENTITY.md, writes a RENAME_NOTICE.md, and
// clears conversation history (memory.db) so stale context doesn't
// override the new identity.
func writeIdentityChange(agentDir, oldName, newName string) {
	ws := filepath.Join(agentDir, "data", "workspace")

	// Overwrite IDENTITY.md with the new name.
	identity := fmt.Sprintf(
		"# Identity\n\n"+
			"- **Name:** %s\n"+
			"- **Previous name:** %s\n"+
			"- **Renamed:** %s\n",
		newName, oldName,
		time.Now().UTC().Format(time.RFC3339),
	)
	if err := os.WriteFile(filepath.Join(ws, "IDENTITY.md"), []byte(identity), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "warning: write identity file: %v\n", err)
	}

	// Remove RENAME_NOTICE from previous renames (best-effort cleanup).
	_ = os.Remove(filepath.Join(ws, "RENAME_NOTICE.md"))

}

// updateSystemPromptName replaces the agent's old name with the new name
// in the system_prompt config field.
func updateSystemPromptName(agentDir, oldName, newName string) {
	val, err := ConfigGet(agentDir, "system_prompt")
	if err != nil {
		return
	}
	prompt, ok := val.(string)
	if !ok || prompt == "" {
		return
	}

	updated := strings.ReplaceAll(prompt, oldName, newName)
	if updated != prompt {
		ConfigSet(agentDir, "system_prompt", updated)
	}
}
