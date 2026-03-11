package cli

import (
	"github.com/jotavich/xnullclaw/internal/agent"
)

func cmdClone(g Globals, args []string) {
	withData := hasFlag(&args, "--with-data")
	names := agentNames(args)

	if len(names) < 2 {
		die("usage: xnc clone <source> <new> [--with-data]")
	}

	src := names[0]
	dst := names[1]

	if err := agent.Clone(g.Home, src, dst, agent.CloneOpts{WithData: withData}); err != nil {
		die("%v", err)
	}

	dir := agent.Dir(g.Home, dst)
	meta, _ := agent.ReadMeta(dir)
	ok("cloned %s → %s %s", src, meta["EMOJI"], dst)
}
