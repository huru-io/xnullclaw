package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// EmojiPool is the fixed set of 40 emojis available for agent identity.
var EmojiPool = []string{
	// Fruits
	"🍎", "🍊", "🍋", "🍇", "🍓", "🫐", "🍑", "🥝", "🍒", "🥭",
	// Flowers & Plants
	"🌻", "🌸", "🌵", "🍀", "🌺", "🪻", "🌷", "🪷", "🌿", "🍄",
	// Animals
	"🐙", "🦊", "🐝", "🦉", "🐋", "🦎", "🐺", "🦜", "🐬", "🦋",
	// Cosmic & Elements
	"💎", "🔮", "⭐", "🌙", "❄️", "🔥", "🌈", "⚡", "🪐", "🌊",
}

// NextEmoji returns the first emoji from the pool not already used
// by any agent under home.
func NextEmoji(home string) string {
	used := usedEmojis(home)
	for _, e := range EmojiPool {
		if !used[e] {
			return e
		}
	}
	// All used — return first emoji as fallback.
	return EmojiPool[0]
}

// usedEmojis scans all agent .meta files and collects assigned emojis.
func usedEmojis(home string) map[string]bool {
	used := make(map[string]bool)
	entries, err := os.ReadDir(home)
	if err != nil {
		return used
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		dir := filepath.Join(home, e.Name())
		emoji := ReadMetaKey(dir, "EMOJI", "")
		if emoji != "" {
			used[emoji] = true
		}
	}
	return used
}

const startPort = 3001

// NextPort returns the next available port starting from 3001.
// Scans all agent .meta files to find ports already in use.
func NextPort(home string) int {
	used := usedPorts(home)
	port := startPort
	for used[port] {
		port++
	}
	return port
}

// usedPorts scans all agent .meta files and collects assigned ports.
func usedPorts(home string) map[int]bool {
	used := make(map[int]bool)
	entries, err := os.ReadDir(home)
	if err != nil {
		return used
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		dir := filepath.Join(home, e.Name())
		portStr := ReadMetaKey(dir, "HOST_PORT", "")
		if portStr != "" {
			if p, err := strconv.Atoi(portStr); err == nil {
				used[p] = true
			}
		}
	}
	return used
}

// NamePool provides a list of suggested agent names for setup.
var NamePool = []string{
	"alice", "bob", "charlie", "diana", "echo",
	"felix", "grace", "hank", "iris", "jack",
}

// SuggestName returns the first name from the pool that doesn't
// already exist as an agent.
func SuggestName(home string) string {
	for _, name := range NamePool {
		if !Exists(home, name) {
			return name
		}
	}
	return fmt.Sprintf("agent%d", len(NamePool)+1)
}
