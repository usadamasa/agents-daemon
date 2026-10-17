package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// runStatuslineCmd は statusline を stdin 付きで実行する｡HOME と XDG_STATE_HOME は一時ディレクトリへ
// 向ける｡幅 (COLUMNS) と月額上限 (CLAUDE_STATUSLINE_SPEND_CAP) は呼び出し元の環境を引き継がず､
// 引数で毎回決める (work profile の settings が上限を入れているため､空で「未設定」にする)｡
func runStatuslineCmd(t *testing.T, stdin, columns, spendCap string) (stdout, stderr, stateDir string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_STATE_HOME", root)
	t.Setenv("COLUMNS", columns)
	t.Setenv("CLAUDE_STATUSLINE_SPEND_CAP", spendCap)
	for _, key := range []string{"REX_SESSION", "REX_BLOCK"} {
		t.Setenv(key, "")
	}

	cmd := newStatuslineCmd()
	var out, errBuf bytes.Buffer
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v, want nil (statusline は fail-silent)", err)
	}
	return out.String(), errBuf.String(), filepath.Join(root, "agents-daemon")
}

const statuslineInput = `{"model": {"display_name": "Opus"}, "workspace": {"current_dir": "/tmp"}, "session_id": "sess-1",
	"cost": {"total_cost_usd": 3.79}, "context_window": {"total_input_tokens": 100000, "used_percentage": 50, "context_window_size": 200000}}`

func TestNewStatuslineCmd(t *testing.T) {
	t.Run("3 行を描画し､同じプロセスで context/<sid>.json を書く", func(t *testing.T) {
		stdout, stderr, stateDir := runStatuslineCmd(t, statuslineInput, "200", "")
		if stderr != "" {
			t.Errorf("stderr = %q, want empty", stderr)
		}
		lines := strings.Split(stdout, "\n")
		if len(lines) != 3 {
			t.Fatalf("stdout が %d 行､want 3:\n%q", len(lines), stdout)
		}
		for _, want := range []string{"🌸 Opus 💎", "🧠 100k (50%)", "💰 $3.79", "📁 /tmp"} {
			if !strings.Contains(stdout, want) {
				t.Errorf("stdout に %q が無い:\n%q", want, stdout)
			}
		}
		data, err := os.ReadFile(filepath.Join(stateDir, "context", "sess-1.json"))
		if err != nil {
			t.Fatalf("context file が書かれていない: %v", err)
		}
		if !strings.Contains(string(data), `"used_percentage":50,`) {
			t.Errorf("context file = %q", data)
		}
		if _, err := os.Stat(filepath.Join(stateDir, "cost")); !os.IsNotExist(err) {
			t.Errorf("上限が無いのに cost/ が作られている: %v", err)
		}
	})

	t.Run("非表示でも state file と cost は書く (表示と副作用を分離する)", func(t *testing.T) {
		stdout, _, stateDir := runStatuslineCmd(t, statuslineInput, "48", "10000")
		if stdout != "" {
			t.Errorf("stdout = %q, want empty", stdout)
		}
		if _, err := os.Stat(filepath.Join(stateDir, "context", "sess-1.json")); err != nil {
			t.Errorf("context file が無い: %v", err)
		}
		day := time.Now().Format("2006-01-02")
		if _, err := os.Stat(filepath.Join(stateDir, "cost", day, "sess-1.json")); err != nil {
			t.Errorf("cost file が無い: %v", err)
		}
	})

	t.Run("上限があれば その日と当月の cost を出す", func(t *testing.T) {
		stdout, stderr, _ := runStatuslineCmd(t, statuslineInput, "200", "10000")
		if stderr != "" {
			t.Errorf("stderr = %q, want empty", stderr)
		}
		for _, want := range []string{"📅 $3.79", "🧾 ~$3.79 / $10k (0%)"} {
			if !strings.Contains(stdout, want) {
				t.Errorf("stdout に %q が無い:\n%q", want, stdout)
			}
		}
	})

	t.Run("compact では合算を読まず 1 行だけ出す", func(t *testing.T) {
		stdout, _, _ := runStatuslineCmd(t, statuslineInput, "80", "10000")
		if strings.Contains(stdout, "\n") || !strings.Contains(stdout, "🧠 100k (50%)") || strings.Contains(stdout, "🧾") {
			t.Errorf("compact の出力が違う:\n%q", stdout)
		}
	})

	t.Run("壊れた JSON でも exit 0 で stdout は空｡理由は stderr", func(t *testing.T) {
		stdout, stderr, _ := runStatuslineCmd(t, `{"session_id": `, "200", "")
		if stdout != "" {
			t.Errorf("stdout = %q, want empty", stdout)
		}
		if stderr == "" {
			t.Error("stderr が空｡手で実行したときに理由が分からない")
		}
	})

	t.Run("git リポジトリでは branch を出す", func(t *testing.T) {
		repo := filepath.Join(t.TempDir(), "repo")
		for _, args := range [][]string{
			{"init", "-q", "-b", "feature-x"},
			{"config", "user.email", "t@example.com"},
			{"config", "user.name", "t"},
			{"commit", "-q", "--allow-empty", "-m", "init"},
		} {
			if err := os.MkdirAll(repo, 0o750); err != nil {
				t.Fatal(err)
			}
			if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v\n%s", args, err, out)
			}
		}
		stdout, _, _ := runStatuslineCmd(t, `{"model": {"display_name": "Opus"}, "workspace": {"current_dir": "`+repo+`"}}`, "200", "")
		if !strings.Contains(stdout, "🌿 feature-x ✨") {
			t.Errorf("stdout に branch が無い:\n%q", stdout)
		}
	})

	t.Run("transcript から prompt cache の残り時間を出す", func(t *testing.T) {
		// 5 分前に 1h の cache write があった transcript｡残りは 55 分前後｡
		at := time.Now().Add(-5 * time.Minute).UTC().Format("2006-01-02T15:04:05.000Z")
		transcript := filepath.Join(t.TempDir(), "sess-1.jsonl")
		body := `{"type":"assistant","isSidechain":false,"timestamp":"` + at + `","message":{"id":"msg_1","model":"claude-fable-5-1","usage":{"input_tokens":3,"cache_creation_input_tokens":500,"cache_read_input_tokens":1000,"cache_creation":{"ephemeral_1h_input_tokens":500,"ephemeral_5m_input_tokens":0}}}}` + "\n"
		if err := os.WriteFile(transcript, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		stdout, stderr, _ := runStatuslineCmd(t, `{"model": {"display_name": "Opus"}, "workspace": {"current_dir": "/tmp"}, "session_id": "sess-1", "transcript_path": "`+transcript+`"}`, "200", "")
		if stderr != "" {
			t.Errorf("stderr = %q, want empty", stderr)
		}
		if !strings.Contains(stdout, "⏱ cache 54m") && !strings.Contains(stdout, "⏱ cache 55m") {
			t.Errorf("stdout に cache の残り時間が無い:\n%q", stdout)
		}
	})

	t.Run("transcript が無ければ cache の項目を出さない", func(t *testing.T) {
		stdout, stderr, _ := runStatuslineCmd(t, statuslineInput, "200", "")
		if stderr != "" || strings.Contains(stdout, "⏱") {
			t.Errorf("stdout = %q, stderr = %q", stdout, stderr)
		}
	})
}
