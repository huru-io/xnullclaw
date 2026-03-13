package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseGlobals_Defaults(t *testing.T) {
	args := []string{}
	g := parseGlobals(&args)

	home, _ := os.UserHomeDir()
	wantHome := filepath.Join(home, ".xnc")

	if g.Home != wantHome {
		t.Errorf("Home = %q, want %q", g.Home, wantHome)
	}
	if g.Image != "nullclaw:latest" {
		t.Errorf("Image = %q, want %q", g.Image, "nullclaw:latest")
	}
	if g.JSON {
		t.Error("JSON should be false by default")
	}
	if g.Quiet {
		t.Error("Quiet should be false by default")
	}
	if g.RuntimeMode != "local" {
		t.Errorf("RuntimeMode = %q, want %q", g.RuntimeMode, "local")
	}
	if g.NetworkName != "" {
		t.Errorf("NetworkName = %q, want empty", g.NetworkName)
	}
	if len(args) != 0 {
		t.Errorf("remaining args = %v, want empty", args)
	}
}

func TestParseGlobals_RuntimeFromEnv(t *testing.T) {
	t.Setenv("XNC_RUNTIME", "docker")
	t.Setenv("XNC_NETWORK", "xnc-net")

	args := []string{}
	g := parseGlobals(&args)

	if g.RuntimeMode != "docker" {
		t.Errorf("RuntimeMode = %q, want %q", g.RuntimeMode, "docker")
	}
	if g.NetworkName != "xnc-net" {
		t.Errorf("NetworkName = %q, want %q", g.NetworkName, "xnc-net")
	}
}

func TestParseGlobals_InvalidRuntime(t *testing.T) {
	t.Setenv("XNC_RUNTIME", "banana")

	args := []string{}
	g := parseGlobals(&args)

	if g.RuntimeMode != "local" {
		t.Errorf("RuntimeMode = %q, want %q (fallback for invalid)", g.RuntimeMode, "local")
	}
}

func TestParseGlobals_InvalidNetwork(t *testing.T) {
	t.Setenv("XNC_NETWORK", "bad network!")

	args := []string{}
	g := parseGlobals(&args)

	if g.NetworkName != "" {
		t.Errorf("NetworkName = %q, want empty (rejected invalid)", g.NetworkName)
	}
}

func TestParseGlobals_Home(t *testing.T) {
	args := []string{"--home", "/custom/path"}
	g := parseGlobals(&args)

	if g.Home != "/custom/path" {
		t.Errorf("Home = %q, want %q", g.Home, "/custom/path")
	}
	if len(args) != 0 {
		t.Errorf("remaining args = %v, want empty", args)
	}
}

func TestParseGlobals_Image(t *testing.T) {
	args := []string{"--image", "custom:v1"}
	g := parseGlobals(&args)

	if g.Image != "custom:v1" {
		t.Errorf("Image = %q, want %q", g.Image, "custom:v1")
	}
	if len(args) != 0 {
		t.Errorf("remaining args = %v, want empty", args)
	}
}

func TestParseGlobals_JSON(t *testing.T) {
	args := []string{"--json"}
	g := parseGlobals(&args)

	if !g.JSON {
		t.Error("JSON should be true when --json is passed")
	}
	if len(args) != 0 {
		t.Errorf("remaining args = %v, want empty", args)
	}
}

func TestParseGlobals_Quiet(t *testing.T) {
	// Test both --quiet and -q
	for _, flag := range []string{"--quiet", "-q"} {
		t.Run(flag, func(t *testing.T) {
			args := []string{flag}
			g := parseGlobals(&args)

			if !g.Quiet {
				t.Errorf("Quiet should be true when %s is passed", flag)
			}
			if len(args) != 0 {
				t.Errorf("remaining args = %v, want empty", args)
			}
		})
	}
}

func TestParseGlobals_Mixed(t *testing.T) {
	args := []string{"--home", "/tmp/test", "alice", "--json", "bob", "-q", "--image", "img:v2"}
	g := parseGlobals(&args)

	if g.Home != "/tmp/test" {
		t.Errorf("Home = %q, want %q", g.Home, "/tmp/test")
	}
	if g.Image != "img:v2" {
		t.Errorf("Image = %q, want %q", g.Image, "img:v2")
	}
	if !g.JSON {
		t.Error("JSON should be true")
	}
	if !g.Quiet {
		t.Error("Quiet should be true")
	}
	if len(args) != 2 || args[0] != "alice" || args[1] != "bob" {
		t.Errorf("remaining args = %v, want [alice bob]", args)
	}
}

func TestHasFlag_Present(t *testing.T) {
	args := []string{"foo", "-f", "bar"}
	found := hasFlag(&args, "-f")

	if !found {
		t.Error("hasFlag should return true when flag is present")
	}
	if len(args) != 2 || args[0] != "foo" || args[1] != "bar" {
		t.Errorf("args after removal = %v, want [foo bar]", args)
	}
}

func TestHasFlag_Absent(t *testing.T) {
	args := []string{"foo", "bar"}
	found := hasFlag(&args, "-f")

	if found {
		t.Error("hasFlag should return false when flag is absent")
	}
	if len(args) != 2 || args[0] != "foo" || args[1] != "bar" {
		t.Errorf("args should be unchanged, got %v", args)
	}
}

func TestFlagValue_Present(t *testing.T) {
	args := []string{"foo", "--name", "alice", "bar"}
	val, found := flagValue(&args, "--name")

	if !found {
		t.Error("flagValue should return true when flag is present")
	}
	if val != "alice" {
		t.Errorf("val = %q, want %q", val, "alice")
	}
	if len(args) != 2 || args[0] != "foo" || args[1] != "bar" {
		t.Errorf("args after removal = %v, want [foo bar]", args)
	}
}

func TestFlagValue_Absent(t *testing.T) {
	args := []string{"foo", "bar"}
	val, found := flagValue(&args, "--name")

	if found {
		t.Error("flagValue should return false when flag is absent")
	}
	if val != "" {
		t.Errorf("val = %q, want empty string", val)
	}
	if len(args) != 2 || args[0] != "foo" || args[1] != "bar" {
		t.Errorf("args should be unchanged, got %v", args)
	}
}

func TestAgentNames_FiltersFlags(t *testing.T) {
	args := []string{"alice", "--force", "bob", "-q", "charlie"}
	names := agentNames(args)

	want := []string{"alice", "bob", "charlie"}
	if len(names) != len(want) {
		t.Fatalf("agentNames = %v, want %v", names, want)
	}
	for i, n := range names {
		if n != want[i] {
			t.Errorf("names[%d] = %q, want %q", i, n, want[i])
		}
	}
}
