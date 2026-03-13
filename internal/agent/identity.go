package agent

import (
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strings"
)

// EmojiPool is the set of emojis available for agent identity.
// Groups mirror the telegram notification palette for consistency,
// plus an xnc-original cosmic/elements group. 107 total.
var EmojiPool = []string{
	// Books (6)
	"📕", "📙", "📒", "📗", "📘", "📓",
	// Produce (24)
	"🍎", "🍊", "🍋", "🍌", "🍏", "🍇", "🍉", "🍓", "🍒", "🍑",
	"🥭", "🍍", "🥝", "🫐", "🥥", "🌽", "🥕", "🍆", "🥑", "🌶️",
	"🥒", "🍄", "🌰", "🍐",
	// Animals (27)
	"🐶", "🐱", "🐻", "🦊", "🐼", "🐨", "🦁", "🐯", "🐮", "🐷",
	"🐸", "🐵", "🦉", "🐧", "🐬", "🦋", "🐢", "🐙", "🦎", "🐳",
	"🦞", "🦩", "🐝", "🦈", "🦔", "🐰", "🦜",
	// Garden (12)
	"🫒", "🧄", "🧅", "🍅", "🥦", "🫑", "🥜", "🫘", "🧆", "🫛",
	"🥔", "🫚",
	// Flowers (14)
	"🌹", "🌻", "🌸", "🌺", "🌷", "🪻", "🌵", "🌴", "🌲", "🍀",
	"🪴", "🎋", "🪷", "🌾",
	// Sweets (14)
	"🍩", "🧁", "🍪", "🎂", "🍰", "🍫", "🍬", "🍭", "🧇", "🥐",
	"🥨", "🍯", "🍿", "🧃",
	// Cosmic & Elements (10)
	"💎", "🔮", "⭐", "🌙", "❄️", "🔥", "🌈", "⚡", "🪐", "🌊",
}

// EmojiForName returns a deterministic emoji for the given agent name
// by hashing the canonical form. Same name always yields the same emoji.
func EmojiForName(name string) string {
	cn := CanonicalName(name)
	h := fnv.New32a()
	h.Write([]byte(cn))
	return EmojiPool[int(h.Sum32())%len(EmojiPool)]
}

// NextEmoji returns an emoji for a new agent. It first tries the
// deterministic emoji for the name; if that's taken, falls back to
// the first unused emoji from the pool.
func NextEmoji(home, name string) string {
	used := usedEmojis(home)

	// Prefer deterministic emoji for this name.
	if preferred := EmojiForName(name); !used[preferred] {
		return preferred
	}

	// Fallback: first unused from pool.
	for _, e := range EmojiPool {
		if !used[e] {
			return e
		}
	}
	// All 107 used — combine two emojis for a unique pair.
	return compoundEmoji(name, used)
}

// compoundEmoji generates a two-emoji combo that isn't already used.
// With 107 emojis, this gives 107*106 = 11,342 unique pairs.
func compoundEmoji(name string, used map[string]bool) string {
	cn := CanonicalName(name)
	h := fnv.New32a()
	h.Write([]byte(cn))
	v := h.Sum32()
	n := uint32(len(EmojiPool))

	a := int(v % n)
	b := int((v / n) % n)
	if b >= a {
		b++ // avoid same emoji twice
	}
	b = b % int(n)

	candidate := EmojiPool[a] + EmojiPool[b]
	if !used[candidate] {
		return candidate
	}

	// Brute-force first unused pair.
	for i := 0; i < int(n); i++ {
		for j := 0; j < int(n); j++ {
			if i == j {
				continue
			}
			pair := EmojiPool[i] + EmojiPool[j]
			if !used[pair] {
				return pair
			}
		}
	}
	// Truly exhausted (11k+ agents) — return hash-based pair anyway.
	return candidate
}

// forEachAgentDir iterates over all agent directories under home,
// calling fn with each agent's directory path. Used by usedEmojis.
func forEachAgentDir(home string, fn func(dir string)) {
	agentsDir := AgentsDir(home)
	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		fn(filepath.Join(agentsDir, e.Name()))
	}
}

// usedEmojis scans all agent .meta files and collects assigned emojis.
func usedEmojis(home string) map[string]bool {
	used := make(map[string]bool)
	forEachAgentDir(home, func(dir string) {
		if emoji := ReadMetaKey(dir, "EMOJI", ""); emoji != "" {
			used[emoji] = true
		}
	})
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
