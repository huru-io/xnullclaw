package cli

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/jotavich/xnullclaw/internal/agent"
)

func cmdCpTo(g Globals, args []string) {
	if len(args) < 2 {
		die("usage: xnc cp-to <agent> <host-file> [container-dest]")
	}

	agentName := args[0]
	hostPath := args[1]
	destDir := "/nullclaw-data/inbox"
	if len(args) >= 3 {
		destDir = args[2]
	}

	if err := agent.ValidateName(agentName); err != nil {
		die("%v", err)
	}

	g.ensureDocker()
	ctx := context.Background()
	cn := agent.ContainerName(g.Home, agentName)

	// Ensure dest dir exists.
	if _, err := g.Docker.ExecSync(ctx, cn, []string{"mkdir", "-p", destDir}, nil); err != nil {
		die("mkdir %s: %v", destDir, err)
	}

	// Read host file and pack into tar.
	data, err := os.ReadFile(hostPath)
	if err != nil {
		die("read %s: %v", hostPath, err)
	}

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	hdr := &tar.Header{
		Name: filepath.Base(hostPath),
		Mode: 0644,
		Size: int64(len(data)),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		die("tar: %v", err)
	}
	if _, err := tw.Write(data); err != nil {
		die("tar: %v", err)
	}
	if err := tw.Close(); err != nil {
		die("tar close: %v", err)
	}

	if err := g.Docker.CopyToContainer(ctx, cn, destDir, &buf); err != nil {
		die("copy: %v", err)
	}

	fmt.Printf("copied %s → %s:%s/%s\n", hostPath, agentName, destDir, filepath.Base(hostPath))
}

func cmdCpFrom(g Globals, args []string) {
	if len(args) < 2 {
		die("usage: xnc cp-from <agent> <container-path> [host-dest]")
	}

	agentName := args[0]
	containerPath := args[1]
	hostDest := "."
	if len(args) >= 3 {
		hostDest = args[2]
	}

	if err := agent.ValidateName(agentName); err != nil {
		die("%v", err)
	}

	g.ensureDocker()
	ctx := context.Background()
	cn := agent.ContainerName(g.Home, agentName)

	rc, err := g.Docker.CopyFromContainer(ctx, cn, containerPath)
	if err != nil {
		die("copy from %s:%s: %v", agentName, containerPath, err)
	}
	defer rc.Close()

	tr := tar.NewReader(rc)
	hdr, err := tr.Next()
	if err != nil {
		die("tar read: %v", err)
	}

	data, err := io.ReadAll(tr)
	if err != nil {
		die("read: %v", err)
	}

	// Determine output path.
	outPath := hostDest
	info, statErr := os.Stat(hostDest)
	if statErr == nil && info.IsDir() {
		outPath = filepath.Join(hostDest, hdr.Name)
	}

	if err := os.WriteFile(outPath, data, 0644); err != nil {
		die("write %s: %v", outPath, err)
	}

	fmt.Printf("copied %s:%s → %s\n", agentName, containerPath, outPath)
}
