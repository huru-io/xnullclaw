package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/jotavich/xnullclaw/internal/docker"
)

const (
	nullclawRepoURL   = "https://github.com/nullclaw/nullclaw.git"
	nullclawRegistry  = "ghcr.io/nullclaw/nullclaw"
	nullclawLatestRef = "ghcr.io/nullclaw/nullclaw:latest"
)

func cmdImage(g Globals, args []string) {
	if len(args) == 0 {
		die("usage: xnc image <build|update|status>")
	}

	subcmd := args[0]
	rest := args[1:]

	switch subcmd {
	case "status":
		cmdImageStatus(g)
	case "build":
		cmdImageBuild(g, rest)
	case "update":
		cmdImageUpdate(g, rest)
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
		fmt.Println("Get it:  xnc image build")
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

// cmdImageBuild gets the nullclaw Docker image.
// Default: pull from ghcr.io and tag as the local image name.
// --from-source: clone repo and build locally (slow, compiles Zig).
func cmdImageBuild(g Globals, args []string) {
	fromSource := hasFlag(&args, "--from-source")

	g.ensureDocker()
	ctx := context.Background()

	if fromSource {
		buildFromSource(g, ctx, false)
		return
	}

	// Try pulling from registry first.
	info("pulling %s ...", nullclawLatestRef)
	if err := g.Docker.ImagePull(ctx, nullclawLatestRef); err != nil {
		info("pull failed: %v", err)
		info("falling back to building from source...")
		buildFromSource(g, ctx, false)
		return
	}

	// Tag as the local image name if different from the registry ref.
	if g.Image != nullclawLatestRef && g.Image != nullclawRegistry+":latest" {
		if err := g.Docker.ImageTag(ctx, nullclawLatestRef, g.Image); err != nil {
			die("tag image: %v", err)
		}
	}

	ok("image '%s' ready (pulled from registry)", g.Image)
}

// cmdImageUpdate updates the nullclaw Docker image.
// Default: pull latest from ghcr.io.
// --from-source: git pull + rebuild with no cache.
func cmdImageUpdate(g Globals, args []string) {
	fromSource := hasFlag(&args, "--from-source")

	g.ensureDocker()
	ctx := context.Background()

	if fromSource {
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

		buildFromSource(g, ctx, true)
		return
	}

	// Pull latest from registry.
	info("pulling %s ...", nullclawLatestRef)
	if err := g.Docker.ImagePull(ctx, nullclawLatestRef); err != nil {
		die("pull failed: %v\nUse --from-source to build locally", err)
	}

	if g.Image != nullclawLatestRef && g.Image != nullclawRegistry+":latest" {
		if err := g.Docker.ImageTag(ctx, nullclawLatestRef, g.Image); err != nil {
			die("tag image: %v", err)
		}
	}

	ok("image '%s' updated (pulled from registry)", g.Image)
}

// buildFromSource clones the repo and builds the Docker image locally.
func buildFromSource(g Globals, ctx context.Context, noCache bool) {
	dir := ensureRepo(g.Home)

	info("building Docker image from source (compiling Zig — may take a few minutes)...")
	err := g.Docker.ImageBuild(ctx, dir, docker.BuildOpts{
		Tags:    []string{g.Image},
		NoCache: noCache,
	})
	if err != nil {
		die("image build failed: %v", err)
	}

	ok("image '%s' built from source", g.Image)
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
