package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jotavich/xnullclaw/internal/agent"
)

func cmdSkill(g Globals, args []string) {
	if len(args) == 0 {
		die("usage: xnc skill <list|install|remove|info> [options]")
	}

	sub := args[0]
	args = args[1:]

	switch sub {
	case "list":
		cmdSkillList(g, args)
	case "install":
		cmdSkillInstall(g, args)
	case "remove", "delete":
		cmdSkillRemove(g, args)
	case "info":
		cmdSkillInfo(g, args)
	default:
		die("unknown skill subcommand: %s\nUsage: xnc skill <list|install|remove|info>", sub)
	}
}

func cmdSkillList(g Globals, args []string) {
	agentName, _ := flagValue(&args, "--agent")
	all := hasFlag(&args, "--all")

	if all {
		// List skills for all agents + shared.
		listSharedSkills(g)
		agents, err := agent.ListAll(g.Home)
		if err != nil {
			die("list agents: %v", err)
		}
		for _, a := range agents {
			listAgentSkills(g, a.Name, a.Emoji)
		}
		return
	}

	if agentName != "" {
		if !agent.Exists(g.Home, agentName) {
			die("agent %q does not exist", agentName)
		}
		listAgentSkills(g, agentName, "")
		return
	}

	// Default: show shared skills.
	listSharedSkills(g)
}

func listSharedSkills(g Globals) {
	dir := agent.SharedSkillsDir(g.Home)
	skills, err := agent.ListSkills(dir)
	if err != nil {
		die("list shared skills: %v", err)
	}

	if g.JSON {
		data, _ := json.MarshalIndent(map[string]any{"scope": "shared", "skills": skills}, "", "  ")
		fmt.Println(string(data))
		return
	}

	if len(skills) == 0 {
		fmt.Println("shared: (no skills installed)")
		return
	}
	fmt.Printf("shared: %d skill(s)\n", len(skills))
	for _, s := range skills {
		printSkillLine(s)
	}
}

func listAgentSkills(g Globals, name, emoji string) {
	dir := agent.AgentSkillsDir(g.Home, name)
	skills, err := agent.ListSkills(dir)
	if err != nil {
		die("list skills for %s: %v", name, err)
	}

	if g.JSON {
		data, _ := json.MarshalIndent(map[string]any{"agent": name, "skills": skills}, "", "  ")
		fmt.Println(string(data))
		return
	}

	label := name
	if emoji != "" {
		label = emoji + " " + name
	}

	if len(skills) == 0 {
		fmt.Printf("%s: (no skills)\n", label)
		return
	}
	fmt.Printf("%s: %d skill(s)\n", label, len(skills))
	for _, s := range skills {
		printSkillLine(s)
	}
}

func printSkillLine(s agent.SkillInfo) {
	parts := []string{"  " + s.Name}
	if s.Version != "" {
		parts = append(parts, "v"+s.Version)
	}
	if s.Description != "" {
		parts = append(parts, "— "+s.Description)
	}
	fmt.Println(strings.Join(parts, " "))
}

func cmdSkillInstall(g Globals, args []string) {
	agentName, _ := flagValue(&args, "--agent")
	all := hasFlag(&args, "--all")

	names := agentNames(args)
	if len(names) == 0 {
		die("usage: xnc skill install <source> [--agent NAME] [--all]")
	}
	source := names[0]

	if agentName != "" {
		// Install to a specific agent.
		if !agent.Exists(g.Home, agentName) {
			die("agent %q does not exist", agentName)
		}
		dir := agent.AgentSkillsDir(g.Home, agentName)
		name, err := agent.InstallSkill(dir, source)
		if err != nil {
			die("install skill: %v", err)
		}
		ok("installed %s for %s", name, agentName)
		return
	}

	// Install to shared.
	sharedDir := agent.SharedSkillsDir(g.Home)
	skillName, err := agent.InstallSkill(sharedDir, source)
	if err != nil {
		die("install skill: %v", err)
	}
	ok("installed %s (shared)", skillName)

	if all {
		// Also copy to all existing agents.
		count, err := agent.SyncSharedToAgents(g.Home, skillName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: sync to agents: %v\n", err)
		}
		if count > 0 {
			ok("synced to %d agent(s)", count)
		}
	}
}

func cmdSkillRemove(g Globals, args []string) {
	agentName, _ := flagValue(&args, "--agent")
	all := hasFlag(&args, "--all")

	names := agentNames(args)
	if len(names) == 0 {
		die("usage: xnc skill remove <name> [--agent NAME] [--all]")
	}
	skillName := names[0]

	if agentName != "" {
		// Remove from specific agent.
		if !agent.Exists(g.Home, agentName) {
			die("agent %q does not exist", agentName)
		}
		dir := agent.AgentSkillsDir(g.Home, agentName)
		if err := agent.RemoveSkill(dir, skillName); err != nil {
			die("%v", err)
		}
		ok("removed %s from %s", skillName, agentName)
		return
	}

	// Remove from shared.
	sharedDir := agent.SharedSkillsDir(g.Home)
	if err := agent.RemoveSkill(sharedDir, skillName); err != nil {
		die("%v", err)
	}
	ok("removed %s (shared)", skillName)

	if all {
		// Also remove from all agents.
		count, err := agent.RemoveSkillFromAllAgents(g.Home, skillName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: remove from agents: %v\n", err)
		}
		if count > 0 {
			ok("removed from %d agent(s)", count)
		}
	}
}

func cmdSkillInfo(g Globals, args []string) {
	agentName, _ := flagValue(&args, "--agent")

	names := agentNames(args)
	if len(names) == 0 {
		die("usage: xnc skill info <name> [--agent NAME]")
	}
	skillName := names[0]

	var dir string
	var scope string
	if agentName != "" {
		if !agent.Exists(g.Home, agentName) {
			die("agent %q does not exist", agentName)
		}
		dir = agent.AgentSkillsDir(g.Home, agentName)
		scope = agentName
	} else {
		dir = agent.SharedSkillsDir(g.Home)
		scope = "shared"
	}

	skillDir := filepath.Join(dir, skillName)
	if _, err := os.Stat(skillDir); os.IsNotExist(err) {
		die("skill %q not found in %s", skillName, scope)
	}

	skills, err := agent.ListSkills(dir)
	if err != nil {
		die("read skills: %v", err)
	}

	for _, s := range skills {
		if s.Name == skillName {
			if g.JSON {
				data, _ := json.MarshalIndent(s, "", "  ")
				fmt.Println(string(data))
				return
			}

			fmt.Printf("Name:        %s\n", s.Name)
			fmt.Printf("Scope:       %s\n", scope)
			if s.Version != "" {
				fmt.Printf("Version:     %s\n", s.Version)
			}
			if s.Description != "" {
				fmt.Printf("Description: %s\n", s.Description)
			}
			if s.Author != "" {
				fmt.Printf("Author:      %s\n", s.Author)
			}
			fmt.Printf("Path:        %s\n", s.Path)
			var files []string
			if s.HasTOML {
				files = append(files, "SKILL.toml")
			}
			if s.HasJSON {
				files = append(files, "skill.json")
			}
			if s.HasMD {
				files = append(files, "SKILL.md")
			}
			fmt.Printf("Files:       %s\n", strings.Join(files, ", "))
			return
		}
	}

	die("skill %q not found in %s", skillName, scope)
}
