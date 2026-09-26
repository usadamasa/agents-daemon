package apppath

import (
	"path/filepath"
	"testing"
)

func TestPaths_StateDir(t *testing.T) {
	tests := []struct {
		name         string
		xdgStateHome string
		want         string
	}{
		{name: "XDG_STATE_HOME があればその配下", xdgStateHome: "/xdg/state", want: "/xdg/state/agents-daemon"},
		{name: "XDG_STATE_HOME が空なら ~/.local/state", xdgStateHome: "", want: "/Users/x/.local/state/agents-daemon"},
		{name: "XDG_STATE_HOME が空白だけでも ~/.local/state", xdgStateHome: "  ", want: "/Users/x/.local/state/agents-daemon"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := New("/Users/x", tc.xdgStateHome, "")
			if got := p.StateDir(); got != tc.want {
				t.Errorf("StateDir() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPaths_ConfigFile(t *testing.T) {
	tests := []struct {
		name          string
		xdgConfigHome string
		want          string
	}{
		{name: "XDG_CONFIG_HOME があればその配下", xdgConfigHome: "/xdg/config", want: "/xdg/config/agents-daemon/config.json"},
		{name: "XDG_CONFIG_HOME が空なら ~/.config", xdgConfigHome: "", want: "/Users/x/.config/agents-daemon/config.json"},
		{name: "XDG_CONFIG_HOME が空白だけでも ~/.config", xdgConfigHome: " \t", want: "/Users/x/.config/agents-daemon/config.json"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// 設定と state の起点は独立している｡XDG_STATE_HOME を指定しても設定の場所は動かない｡
			p := New("/Users/x", "/xdg/state", tc.xdgConfigHome)
			if got := p.ConfigFile(); got != tc.want {
				t.Errorf("ConfigFile() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPaths_RuntimeFilesLiveUnderStateDir(t *testing.T) {
	p := New("/Users/x", "/xdg/state", "/xdg/config")
	state := "/xdg/state/agents-daemon"
	checks := map[string]struct{ got, want string }{
		"PIDFile":    {p.PIDFile(), filepath.Join(state, "daemon.pid")},
		"StatusFile": {p.StatusFile(), filepath.Join(state, "status.json")},
		"LogFile":    {p.LogFile(), filepath.Join(state, "logs", "daemon.log")},
	}
	for name, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", name, c.got, c.want)
		}
	}
}

func TestFromEnv(t *testing.T) {
	t.Run("HOME と XDG_STATE_HOME と XDG_CONFIG_HOME を環境から読む", func(t *testing.T) {
		t.Setenv("HOME", "/Users/y")
		t.Setenv("XDG_STATE_HOME", "/xdg/state")
		t.Setenv("XDG_CONFIG_HOME", "/xdg/config")
		p, err := FromEnv()
		if err != nil {
			t.Fatalf("FromEnv() error = %v", err)
		}
		if got, want := p.ConfigFile(), "/xdg/config/agents-daemon/config.json"; got != want {
			t.Errorf("ConfigFile() = %q, want %q", got, want)
		}
		if got, want := p.StateDir(), "/xdg/state/agents-daemon"; got != want {
			t.Errorf("StateDir() = %q, want %q", got, want)
		}
	})

	t.Run("XDG_* 未設定なら ~/.local/state と ~/.config", func(t *testing.T) {
		t.Setenv("HOME", "/Users/y")
		t.Setenv("XDG_STATE_HOME", "")
		t.Setenv("XDG_CONFIG_HOME", "")
		p, err := FromEnv()
		if err != nil {
			t.Fatalf("FromEnv() error = %v", err)
		}
		if got, want := p.StateDir(), "/Users/y/.local/state/agents-daemon"; got != want {
			t.Errorf("StateDir() = %q, want %q", got, want)
		}
		if got, want := p.ConfigFile(), "/Users/y/.config/agents-daemon/config.json"; got != want {
			t.Errorf("ConfigFile() = %q, want %q", got, want)
		}
	})
}
