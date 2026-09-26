package herdrcli

import (
	"context"
	"strings"
	"testing"
)

// herdr 0.8.0 の send-text / send-keys / report-metadata は、成功したとき
// stdout にも stderr にも何も出さず終了コード 0 だけを返す (実機で確認)。
//
// この応答に envelope を要求していたため、送信が届いているのに毎回失敗と判定され、
// 再試行上限まで突き進んで自動再開が丸ごと機能しない状態になった。実運用で
// 踏んだ回帰なので、現物の応答形をテストに固定しておく。
func TestSendCommands_成功時は無出力で終了コード0(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name string
		call func(Client) error
	}{
		{
			name: "SendText",
			call: func(c Client) error { return c.SendText(ctx, "wH:p1", "resume") },
		},
		{
			name: "SendKeys",
			call: func(c Client) error { return c.SendKeys(ctx, "wH:p1", "esc", "enter") },
		},
		{
			name: "ReportMetadata",
			call: func(c Client) error {
				return c.ReportMetadata(ctx, "wH:p1", ReportMetadataOptions{
					Source:      "agents-daemon",
					StateLabels: map[AgentStatus]string{AgentStatusIdle: "retry engaged"},
				})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name+" は無出力でも成功として扱う", func(t *testing.T) {
			// 何も出力せず 0 で抜ける = herdr の成功時の振る舞い。
			c := newExecClientForTest(t, "exit 0", noCommandTimeout)
			if err := tt.call(c); err != nil {
				t.Errorf("%s() error = %v, want nil (無出力の終了コード 0 は成功)", tt.name, err)
			}
		})

		t.Run(tt.name+" はエラー envelope を返したら失敗にする", func(t *testing.T) {
			script := `echo '{"id":"cli:pane:send","error":{"code":"pane_not_found","message":"pane wH:p1 not found"}}'; exit 1`
			c := newExecClientForTest(t, script, noCommandTimeout)
			err := tt.call(c)
			if err == nil {
				t.Fatalf("%s() error = nil, want error", tt.name)
			}
			if !strings.Contains(err.Error(), "pane_not_found") {
				t.Errorf("%s() error = %v, want エラー envelope の内容を含む", tt.name, err)
			}
		})

		t.Run(tt.name+" は無出力の非 0 終了を失敗にする", func(t *testing.T) {
			c := newExecClientForTest(t, "exit 3", noCommandTimeout)
			if err := tt.call(c); err == nil {
				t.Errorf("%s() error = nil, want error (非 0 終了)", tt.name)
			}
		})
	}
}
