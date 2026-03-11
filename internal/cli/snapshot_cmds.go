package cli

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jotavich/xnullclaw/internal/agent"
	"github.com/jotavich/xnullclaw/internal/docker"
)

func cmdSnapshot(g Globals, args []string) {
	if len(args) == 0 {
		die("usage: xnc snapshot <agent>")
	}

	name := args[0]
	if !agent.Exists(g.Home, name) {
		die("agent %q does not exist", name)
	}

	// Warn if agent is running.
	g.ensureDocker()
	ctx := context.Background()
	cn := agent.ContainerName(g.Home, name)
	running, _ := g.Docker.IsRunning(ctx, cn)
	if running {
		info("stopping %s before snapshot...", name)
		g.Docker.StopContainer(ctx, cn)
		defer func() {
			info("restarting %s...", name)
			g.Docker.StartContainer(ctx, cn, defaultStartOpts(g, name))
		}()
	}

	snap, err := agent.Snapshot(g.Home, name)
	if err != nil {
		die("%v", err)
	}

	ok("snapshot %s %s → %s (%s)", snap.Emoji, snap.Agent, snap.Name, humanSize(snap.SizeBytes))
}

func cmdRestore(g Globals, args []string) {
	names := agentNames(args)
	if len(names) == 0 {
		die("usage: xnc restore <snapshot-name> [new-agent-name]")
	}

	snapName := names[0]
	targetName := ""
	if len(names) > 1 {
		targetName = names[1]
	}

	if err := agent.Restore(g.Home, snapName, targetName); err != nil {
		die("%v", err)
	}

	// Figure out the actual target name for the success message.
	if targetName == "" {
		snapDir := agent.SnapshotDir(g.Home)
		targetName = agent.ReadMetaKey(snapDir+"/"+snapName, "SNAPSHOT_OF", snapName)
		// Re-read from restored agent.
		if agent.Exists(g.Home, targetName) {
			// good
		}
	}
	dir := agent.Dir(g.Home, targetName)
	meta, _ := agent.ReadMeta(dir)
	ok("restored %s %s from %s", meta["EMOJI"], targetName, snapName)
}

func cmdSnapshots(g Globals, args []string) {
	snaps, err := agent.ListSnapshots(g.Home)
	if err != nil {
		die("%v", err)
	}

	if g.JSON {
		data, _ := json.MarshalIndent(snaps, "", "  ")
		fmt.Println(string(data))
		return
	}

	if len(snaps) == 0 {
		info("no snapshots")
		return
	}

	for _, s := range snaps {
		emoji := s.Emoji
		if emoji == "" {
			emoji = " "
		}
		fmt.Printf("  %s %-35s %s (%s)\n", emoji, s.Name, s.Created[:19], humanSize(s.SizeBytes))
	}
	fmt.Printf("\n%d snapshot(s)\n", len(snaps))
}

func cmdSnapshotDelete(g Globals, args []string) {
	if len(args) == 0 {
		die("usage: xnc snapshot-delete <snapshot-name>")
	}

	name := args[0]
	if err := agent.DeleteSnapshot(g.Home, name); err != nil {
		die("%v", err)
	}

	ok("deleted snapshot %s", name)
}

// defaultStartOpts returns basic ContainerOpts for restarting an agent after snapshot.
func defaultStartOpts(g Globals, name string) docker.ContainerOpts {
	return docker.ContainerOpts{
		Image:    g.Image,
		Cmd:      []string{"agent"},
		AgentDir: agent.Dir(g.Home, name),
	}
}
