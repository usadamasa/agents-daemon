package herdrcli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// writeFakeHerdr は HERDR_BIN_PATH に指せる shell script を作り､そのパスを返す｡
// 実 herdr を使わずに exec 経路 (タイムアウト・非 0 exit・stdout/stderr 分離・
// 引数の組み立て) を検証するため､実行可能な fake として振る舞わせる｡
func writeFakeHerdr(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "herdr")
	body := "#!/bin/sh\n" + script + "\n"
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("fake herdr script の作成に失敗: %v", err)
	}
	return path
}

// noCommandTimeout はタイムアウトを課さないことを表す｡マシンの負荷で fake の
// shell 起動が遅れただけでテストが落ちる (実際に落ちた) のを､制限時間を延ばして
// 誤魔化すのではなく､時間の制約そのものを外して断ち切る｡タイムアウトの挙動を
// 検証するテストだけが､意図的に短い値を渡す｡
const noCommandTimeout = 0

func newExecClientForTest(t *testing.T, script string, timeout time.Duration) *execClient {
	t.Helper()
	return &execClient{binPath: writeFakeHerdr(t, script), timeout: timeout}
}

func TestExecClient_PaneList_Success(t *testing.T) {
	fixture := `{"id":"cli:pane:list","result":{"panes":[{"agent":"claude","agent_session":{"agent":"claude","kind":"id","source":"herdr:claude","value":"0d5e717f-82c5-4e71-ac74-799f7197e0de"},"agent_status":"working","cwd":"/x","focused":true,"foreground_cwd":"/x","pane_id":"wH:p1","revision":76,"scroll":{"max_offset_from_bottom":0,"offset_from_bottom":0,"viewport_rows":37},"tab_id":"wH:t1","terminal_id":"term_1","terminal_title":"t","terminal_title_stripped":"t","workspace_id":"wH"},{"agent_status":"unknown","cwd":"/x","focused":false,"foreground_cwd":"/x","pane_id":"wH:p4","revision":5,"scroll":{"max_offset_from_bottom":0,"offset_from_bottom":0,"viewport_rows":37},"tab_id":"wH:t4","terminal_id":"term_2","terminal_title":"s","terminal_title_stripped":"s","workspace_id":"wH"}],"type":"pane_list"}}`
	c := newExecClientForTest(t, "cat <<'EOF'\n"+fixture+"\nEOF", noCommandTimeout)

	panes, err := c.PaneList(context.Background())
	if err != nil {
		t.Fatalf("PaneList() error = %v", err)
	}
	if len(panes) != 2 {
		t.Fatalf("len(panes) = %d, want 2", len(panes))
	}
	if !IsClaudeAgent(panes[0]) {
		t.Errorf("panes[0] は claude agent であるべき")
	}
	if IsClaudeAgent(panes[1]) {
		t.Errorf("panes[1] (shell pane) は claude agent ではないべき")
	}
}

func TestExecClient_PaneGet_ErrorEnvelopeSurfacesAsError(t *testing.T) {
	// pane_not_found を実機で確認したそのままの応答: exit 1 + JSON envelope｡
	script := `echo '{"error":{"code":"pane_not_found","message":"pane nonexistent-pane-id not found"},"id":"cli:pane:get"}'
exit 1`
	c := newExecClientForTest(t, script, noCommandTimeout)

	_, err := c.PaneGet(context.Background(), "nonexistent-pane-id")
	if err == nil {
		t.Fatalf("PaneGet() error = nil, want error")
	}
	var envErr *envelopeError
	if !errors.As(err, &envErr) {
		t.Fatalf("error = %v, want *envelopeError", err)
	}
	if envErr.Code != "pane_not_found" {
		t.Errorf("envErr.Code = %q, want %q", envErr.Code, "pane_not_found")
	}
}

func TestExecClient_Run_MalformedOutputAndNonZeroExit(t *testing.T) {
	script := `echo "boom" 1>&2
exit 3`
	c := newExecClientForTest(t, script, noCommandTimeout)

	_, err := c.PaneGet(context.Background(), "wH:p1")
	if err == nil {
		t.Fatalf("PaneGet() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error = %v, want it to mention stderr content", err)
	}
}

func TestExecClient_Run_Timeout(t *testing.T) {
	script := `sleep 2`
	c := newExecClientForTest(t, script, 50*time.Millisecond)

	start := time.Now()
	_, err := c.PaneGet(context.Background(), "wH:p1")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("PaneGet() error = nil, want timeout error")
	}
	if !strings.Contains(err.Error(), "タイムアウト") {
		t.Errorf("error = %v, want it to mention タイムアウト", err)
	}
	if elapsed > time.Second {
		t.Errorf("elapsed = %v, want it to be bounded by the timeout, not the sleep duration", elapsed)
	}
}

func TestExecClient_PaneRead_ReturnsRawText(t *testing.T) {
	// pane read は envelope に包まれない生テキストを返す｡JSON parse を試みてはいけない｡
	rawText := "  ⏺ main\n  ◯ impl-herdrcli\n"
	script := "printf '%s' " + shellQuote(rawText)
	c := newExecClientForTest(t, script, noCommandTimeout)

	got, err := c.PaneRead(context.Background(), "wH:p1", PaneReadOptions{Source: SourceDetection, Lines: 5})
	if err != nil {
		t.Fatalf("PaneRead() error = %v", err)
	}
	if got != rawText {
		t.Errorf("PaneRead() = %q, want %q", got, rawText)
	}
}

func TestExecClient_PaneRead_ArgsIncludeSourceAndLines(t *testing.T) {
	capturePath := filepath.Join(t.TempDir(), "args.txt")
	t.Setenv("CAPTURE_FILE", capturePath)
	c := newExecClientForTest(t, captureArgsScript, noCommandTimeout)

	if _, err := c.PaneRead(context.Background(), "wH:p1", PaneReadOptions{Source: SourceDetection, Lines: 12}); err != nil {
		t.Fatalf("PaneRead() error = %v", err)
	}
	got := readCapturedArgs(t, capturePath)
	want := []string{"pane", "read", "wH:p1", "--source", "detection", "--lines", "12"}
	if !slices.Equal(got, want) {
		t.Errorf("captured args = %v, want %v", got, want)
	}
}

func TestExecClient_ReportMetadata_RequiresSource(t *testing.T) {
	c := newExecClientForTest(t, "echo should-not-run; exit 1", noCommandTimeout)

	err := c.ReportMetadata(context.Background(), "wH:p1", ReportMetadataOptions{})
	if err == nil {
		t.Fatalf("ReportMetadata() error = nil, want error for missing Source")
	}
}

func TestExecClient_ReportMetadata_BuildsArgsDeterministically(t *testing.T) {
	capturePath := filepath.Join(t.TempDir(), "args.txt")
	t.Setenv("CAPTURE_FILE", capturePath)
	c := newExecClientForTest(t, captureArgsScript+"\necho '{\"id\":\"cli:request\",\"result\":{}}'", noCommandTimeout)

	opts := ReportMetadataOptions{
		Source: "agents-daemon",
		StateLabels: map[AgentStatus]string{
			AgentStatusIdle:    "waiting",
			AgentStatusBlocked: "waiting",
		},
		TTLMillis: 5000,
	}
	if err := c.ReportMetadata(context.Background(), "wH:p1", opts); err != nil {
		t.Fatalf("ReportMetadata() error = %v", err)
	}

	got := readCapturedArgs(t, capturePath)
	want := []string{
		"pane", "report-metadata", "wH:p1",
		"--source", "agents-daemon",
		"--state-label", "blocked=waiting",
		"--state-label", "idle=waiting",
		"--ttl-ms", "5000",
	}
	if !slices.Equal(got, want) {
		t.Errorf("captured args = %v, want %v", got, want)
	}
}

func TestExecClient_Reachable(t *testing.T) {
	tests := []struct {
		name    string
		script  string
		wantErr bool
	}{
		{
			name:   "server running なら nil",
			script: `echo '{"client":{"version":"0.8.0"},"server":{"status":"running","running":true,"socket":"/x.sock"}}'`,
		},
		{
			name:    "server running=false なら error",
			script:  `echo '{"client":{"version":"0.8.0"},"server":{"status":"stopped","running":false}}'`,
			wantErr: true,
		},
		{
			name:    "壊れた JSON なら error",
			script:  `echo 'not json'`,
			wantErr: true,
		},
		{
			name:    "非 0 exit なら error",
			script:  "echo 'connection refused' 1>&2\nexit 1",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newExecClientForTest(t, tt.script, noCommandTimeout)
			err := c.Reachable(context.Background())
			if tt.wantErr && err == nil {
				t.Fatalf("Reachable() error = nil, want error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("Reachable() error = %v, want nil", err)
			}
		})
	}
}

func TestNewExecClient_UsesHerdrBinPathOverride(t *testing.T) {
	t.Setenv("HERDR_BIN_PATH", "/custom/path/to/herdr")
	c := NewExecClient().(*execClient)
	if c.binPath != "/custom/path/to/herdr" {
		t.Errorf("binPath = %q, want %q", c.binPath, "/custom/path/to/herdr")
	}
}

func TestNewExecClient_DefaultsToHerdrOnPath(t *testing.T) {
	t.Setenv("HERDR_BIN_PATH", "")
	c := NewExecClient().(*execClient)
	if c.binPath != "herdr" {
		t.Errorf("binPath = %q, want %q", c.binPath, "herdr")
	}
}

// captureArgsScript は受け取った引数をそのまま 1 行 1 引数で CAPTURE_FILE へ書き出す｡
// echo "$@" は引数間のスペースと引数内のスペースを区別できないため､
// 値にスペースを含むケース (--title の日本語文言等) を正しく検証するために for ループで書く｡
const captureArgsScript = `: > "$CAPTURE_FILE"
for a in "$@"; do
  printf '%s\n' "$a" >> "$CAPTURE_FILE"
done`

func readCapturedArgs(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("captured args の読み込みに失敗: %v", err)
	}
	s := strings.TrimRight(string(b), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// shellQuote は sh の単一引用符リテラルとして安全な文字列に変換する｡
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
