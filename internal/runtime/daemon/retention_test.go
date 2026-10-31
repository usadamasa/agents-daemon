package daemon

// 依存図とパーキングロットが読む記録の保持期間の表と、その間の関係。値と根拠は記録を書くパッケージの定数が持ち、
// ここは関係だけを固定する (どの定数を変えても、関係が崩れればここで落ちる)。表は architecture.md の
// 「出力されるファイル」の消す主体の列と同じ並び。

import (
	"testing"
	"time"

	"github.com/usadamasa/agents-daemon/internal/edge"
	"github.com/usadamasa/agents-daemon/internal/ledger"
	"github.com/usadamasa/agents-daemon/internal/parking"
	"github.com/usadamasa/agents-daemon/internal/sessioncontext"
	"github.com/usadamasa/agents-daemon/internal/tasklog"
)

func TestRetention_関係(t *testing.T) {
	const day = 24 * time.Hour
	// 表。値を変えたらここと architecture.md を直す。
	table := []struct {
		name string
		got  time.Duration
		want time.Duration
	}{
		{"完了したタスク (tasklog.CompletedRetention)", tasklog.CompletedRetention, day},
		{"タスクと TodoWrite のリスト (tasklog.Retention)", tasklog.Retention, 7 * day},
		{"セッションをまたぐ辺 (edge.Retention)", edge.Retention, 7 * day},
		{"セッション台帳 (ledger.Retention)", ledger.Retention, 7 * day},
		{"セッション名 (sessioncontext.Retention)", sessioncontext.Retention, 7 * day},
		{"open のメモ (parking.OpenRetention)", parking.OpenRetention, 90 * day},
		{"handed / discarded のメモ (parking.ClosedRetention)", parking.ClosedRetention, 7 * day},
		{"古いメモ (parking.StaleAfter、pane のまとめて捨てるボタン)", parking.StaleAfter, 30 * day},
	}
	for _, r := range table {
		if r.got != r.want {
			t.Errorf("%s = %v, want %v (値を変えたら表と architecture.md を直す)", r.name, r.got, r.want)
		}
	}

	relations := []struct {
		name string
		ok   bool
	}{
		// 完了したタスクは、未完了のタスクより先に消える (Show completed は今日の進み具合だけ)。
		{"tasklog.CompletedRetention <= tasklog.Retention", tasklog.CompletedRetention <= tasklog.Retention},
		// 辺は、端点のタスクのノードが依存図に出うる間 (終了したセッションの未完了のタスク) は残る。
		{"edge.Retention >= tasklog.Retention", edge.Retention >= tasklog.Retention},
		// 台帳は、辺が残っている間はその端点を結びつける候補のセッションを引ける。
		{"ledger.Retention >= edge.Retention", ledger.Retention >= edge.Retention},
		// セッション名は、終了したセッションのタスクのノードが出ている間は表示名と置き場の名前に引ける。
		{"sessioncontext.Retention >= tasklog.Retention", sessioncontext.Retention >= tasklog.Retention},
		// 渡した・捨てたメモは、description に id を書いたタスクのノードが出ている間は詳細で照合できる。
		{"parking.ClosedRetention >= tasklog.Retention", parking.ClosedRetention >= tasklog.Retention},
		// 古いメモのボタンは、housekeeping が open のメモを黙って消すより前に出る。
		{"parking.StaleAfter < parking.OpenRetention", parking.StaleAfter < parking.OpenRetention},
		// 古いメモは、片付けたメモが消える期間より長く置かれたもの (置いてすぐのメモを古いと呼ばない)。
		{"parking.StaleAfter > parking.ClosedRetention", parking.StaleAfter > parking.ClosedRetention},
	}
	for _, r := range relations {
		if !r.ok {
			t.Errorf("保持期間の関係が崩れた: %s", r.name)
		}
	}
}
