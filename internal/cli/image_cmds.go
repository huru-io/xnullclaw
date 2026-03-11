package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/jotavich/xnullclaw/internal/docker"
)

const nullclawRepoURL = "https://github.com/nullclaw/nullclaw.git"

func cmdImage(g Globals, args []string) {
	if len(args) == 0 {
		die("usage: xnc image <build|update|status>")
	}

	subcmd := args[0]

	switch subcmd {
	case "status":
		cmdImageStatus(g)
	case "build":
		cmdImageBuild(g)
	case "update":
		cmdImageUpdate(g)
	default:
		die("unknown image subcommand: %s", subcmd)
	}
}

func cmdImageStatus(g Globals) {
	g.ensureDocker()
	ctx := context.Background()

	exists, err := g.Docker.ImageExists(ctx, g.Image)
	if err != nil {
		die("image check: %v", err)
	}

	if !exists {
		fmt.Printf("image %s: not found\n", g.Image)
		fmt.Println("Build it:  xnc image build")
		return
	}

	img, err := g.Docker.ImageInspect(ctx, g.Image)
	if err != nil {
		die("image inspect: %v", err)
	}

	fmt.Printf("Image:   %s\n", g.Image)
	fmt.Printf("ID:      %s\n", img.ID)
	fmt.Printf("Size:    %s\n", humanSize(img.Size))
	fmt.Printf("Created: %s\n", img.Created.Format("2006-01-02 15:04:05"))
	if len(img.Tags) > 0 {
		fmt.Printf("Tags:    %s\n", img.Tags)
	}
	fmt.Println()
	fmt.Println("Security hardening:")
	for _, f := range docker.SecurityFlags() {
		fmt.Printf("  ✓ %s\n", f)
	}
}

// buildDir returns the path to the nullclaw source clone under home.
func buildDir(home string) string {
	return filepath.Join(home, ".tmp", "nullclaw")
}

// ensureRepo clones or reuses the nullclaw repo in the build directory.
// Returns the build directory path.
func ensureRepo(home string) string {
	dir := buildDir(home)
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		info("using existing repo at %s", dir)
		return dir
	}

	if err := os.MkdirAll(filepath.Dir(dir), 0755); err != nil {
		die("create tmp dir: %v", err)
	}

	info("cloning nullclaw...")
	cmd := exec.Command("git", "clone", nullclawRepoURL, dir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		die("git clone failed: %v", err)
	}
	return dir
}

func cmdImageBuild(g Globals) {
	g.ensureDocker()
	ctx := context.Background()

	dir := ensureRepo(g.Home)

	info("building Docker image (compiling Zig — may take a few minutes)...")
	err := g.Docker.ImageBuild(ctx, dir, docker.BuildOpts{
		Tags: []string{g.Image},
	})
	if err != nil {
		die("image build failed: %v", err)
	}

	ok("image '%s' built", g.Image)
}

func cmdImageUpdate(g Globals) {
	g.ensureDocker()
	ctx := context.Background()

	dir := buildDir(g.Home)

	// Pull latest or clone fresh.
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		info("pulling latest changes...")
		cmd := exec.Command("git", "-C", dir, "pull", "--ff-only")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			die("git pull failed: %v", err)
		}
	} else {
		ensureRepo(g.Home)
	}

	info("rebuilding Docker image (no cache)...")
	err := g.Docker.ImageBuild(ctx, dir, docker.BuildOpts{
		Tags:    []string{g.Image},
		NoCache: true,
	})
	if err != nil {
		die("image rebuild failed: %v", err)
	}

	ok("image '%s' updated", g.Image)
}

func humanSize(bytes int64) string {
	const (
		mb = 1024 * 1024
		gb = 1024 * mb
	)
	switch {
	case bytes >= gb:
		return fmt.Sprintf("%.1f GB", float64(bytes)/float64(gb))
	case bytes >= mb:
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(mb))
	default:
		return fmt.Sprintf("%d bytes", bytes)
	}
}
