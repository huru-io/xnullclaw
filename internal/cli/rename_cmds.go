package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jotavich/xnullclaw/internal/agent"
)

func cmdRename(g Globals, args []string) {
	names := agentNames(args)
	if len(names) < 2 {
		die("usage: xnc rename <old-name> <new-name>")
	}

	oldName := names[0]
	newName := names[1]

	if !agent.Exists(g.Home, oldName) {
		die("agent %q does not exist", oldName)
	}

	// Ensure agent is not running.
	g.ensureDocker()
	ctx := context.Background()
	cn := agent.ContainerName(g.Home, oldName)
	running, _ := g.Docker.IsRunning(ctx, cn)
	if running {
		die("agent %q is running — stop it first with: xnc stop %s", oldName, oldName)
	}

	// Get old meta for display.
	oldDir := agent.Dir(g.Home, oldName)
	oldMeta, _ := agent.ReadMeta(oldDir)
	emoji := oldMeta["EMOJI"]

	if err := agent.Rename(g.Home, oldName, newName); err != nil {
		die("%v", err)
	}

	ok("renamed %s %s → %s", emoji, oldName, newName)

	// Start agent and send identity-change message.
	if agent.HasProviderKey(g.Home, newName) {
		newCN := agent.ContainerName(g.Home, newName)
		opts := agent.StartOpts(g.Image, g.Home, newName, true, g.NetworkName)
		if err := g.Docker.StartContainer(ctx, newCN, opts); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not start %s: %v\n", newName, err)
		} else {
			ok("started %s %s", emoji, newName)

			// Wait for gateway readiness before sending identity message.
			port, _ := g.Docker.MappedPort(ctx, newCN)
			baseURL := agent.AgentBaseURL(g.RuntimeMode, port, newCN)
			if err := agent.WaitForHealthy(ctx, baseURL, 30*time.Second); err != nil {
				fmt.Fprintf(os.Stderr, "warning: gateway health check: %v\n", err)
			}

			msg := agent.IdentityChangeMessage(oldName, newName)
			var resp string
			wr, wErr := agent.TrySendWebhook(ctx, g.RuntimeMode, port, newCN, g.Home, newName, msg)
			if wErr != nil {
				fmt.Fprintf(os.Stderr, "warning: identity message failed: %v\n", wErr)
			} else if wr != nil {
				resp = wr.Response
			} else {
				// Fallback: docker exec (legacy containers without port mapping).
				var execErr error
				resp, execErr = g.Docker.ExecSync(ctx, newCN,
					[]string{"flock", "/tmp/.send.lock", "nullclaw", "agent", "-s", "mux"},
					strings.NewReader(msg),
				)
				if execErr != nil {
					fmt.Fprintf(os.Stderr, "warning: identity message failed: %v\n", execErr)
				}
			}
			if resp != "" {
				info("%s acknowledged: %s", newName, strings.TrimSpace(resp))
			}
		}
	}

	// Hint about mux config if applicable.
	muxCfgPath := fmt.Sprintf("%s/mux/config.json", g.Home)
	if data, err := os.ReadFile(muxCfgPath); err == nil {
		if strings.Contains(string(data), oldName) {
			fmt.Fprintf(os.Stderr, "note: update mux config manually if %s is referenced in auto_start/mux_managed/identities\n", oldName)
		}
	}
}
