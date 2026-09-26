package detect

import (
	"testing"
	"time"
)

// 上限の画面に出る `resets 8:10am (Asia/Tokyo)` のような絶対時刻を読む｡
// statusline の resets_at (epoch) が無いセッション (上限中に開いた新規セッション､
// 429 で弾かれた turn は state を書かない) の起床時刻はここから取る｡
//
// 実際の時刻の数字をこのファイルに書いてよいのは､テストコードは pane に表示され
// にくく､表示されても Classify の二重シグナルを満たさない断片だけにしてあるため｡
// README には数字を書かない (real_sample_test.go と同じ理由)｡

var tokyo = time.FixedZone("Asia/Tokyo", 9*60*60)

func jst(y int, m time.Month, d, hh, mm int) time.Time {
	return time.Date(y, m, d, hh, mm, 0, 0, tokyo)
}

func TestParseAbsoluteReset(t *testing.T) {
	tests := []struct {
		name   string
		screen string
		now    time.Time
		want   time.Time
		ok     bool
	}{
		{
			name:   "今日の未来 (07:35 に 8:10am)",
			screen: "⏺ You've hit your session limit · resets 8:10am (Asia/Tokyo)",
			now:    jst(2026, 8, 28, 7, 35),
			want:   jst(2026, 8, 28, 8, 10),
			ok:     true,
		},
		{
			name:   "日付をまたぐ (23:30 に 1:10am は明日)",
			screen: "⏺ You've hit your session limit · resets 1:10am (Asia/Tokyo)",
			now:    jst(2026, 8, 27, 23, 30),
			want:   jst(2026, 8, 28, 1, 10),
			ok:     true,
		},
		{
			name:   "既に過ぎた時刻は今日のまま返す (09:00 に 8:10am､明日の 8:10 は 6 時間より先)",
			screen: "⏺ You've hit your session limit · resets 8:10am (Asia/Tokyo)",
			now:    jst(2026, 8, 28, 9, 0),
			want:   jst(2026, 8, 28, 8, 10),
			ok:     true,
		},
		{
			name:   "12:00am は 0 時",
			screen: "⏺ You've hit your session limit · resets 12:00am (Asia/Tokyo)",
			now:    jst(2026, 8, 27, 23, 0),
			want:   jst(2026, 8, 28, 0, 0),
			ok:     true,
		},
		{
			name:   "12:30pm は 12 時 30 分",
			screen: "⏺ You've hit your session limit · resets 12:30pm (Asia/Tokyo)",
			now:    jst(2026, 8, 16, 10, 0),
			want:   jst(2026, 8, 16, 12, 30),
			ok:     true,
		},
		{
			name:   "分の無い形 (3pm)",
			screen: "Claude usage limit reached.\nYour limit resets at 3pm.",
			now:    jst(2026, 8, 16, 10, 0),
			want:   jst(2026, 8, 16, 15, 0),
			ok:     true,
		},
		{
			name:   "tz 表記が無ければ local (ここでは Asia/Tokyo) で解釈する",
			screen: "⏺ You've hit your session limit · resets 8:10am",
			now:    jst(2026, 8, 28, 7, 35),
			want:   jst(2026, 8, 28, 8, 10),
			ok:     true,
		},
		{
			name:   "解決できない tz 名は local へ落とす",
			screen: "⏺ You've hit your session limit · resets 8:10am (Mars/Olympus)",
			now:    jst(2026, 8, 28, 7, 35),
			want:   jst(2026, 8, 28, 8, 10),
			ok:     true,
		},
		{
			name:   "am/pm の無い 24 時間表記は読まない",
			screen: "Claude usage limit reached.\nYour limit resets at 15:00.",
			now:    jst(2026, 8, 16, 10, 0),
			ok:     false,
		},
		{
			name:   "相対表現だけの画面は読まない",
			screen: "Try again in 5 minutes",
			now:    jst(2026, 8, 16, 10, 0),
			ok:     false,
		},
		{
			name:   "使用率の警告行だけでは読まない (上限到達ではない)",
			screen: "You've used 85% of your 5-hour limit · resets 8:10am (Asia/Tokyo)",
			now:    jst(2026, 8, 28, 7, 35),
			ok:     false,
		},
		{
			name: "複数あれば最も下 (最新の描画) を採る",
			screen: "⏺ You've hit your session limit · resets 3:10am (Asia/Tokyo)\n" +
				"⏺ Continue where you left off.\n" +
				"⏺ You've hit your session limit · resets 8:10am (Asia/Tokyo)",
			now:  jst(2026, 8, 28, 7, 35),
			want: jst(2026, 8, 28, 8, 10),
			ok:   true,
		},
		{
			name: "limit 行から離れすぎた reset 行は使わない",
			screen: "⏺ You've hit your session limit\n" +
				"1\n2\n3\n4\n5\n6\n7\n" +
				"resets 8:10am (Asia/Tokyo)",
			now: jst(2026, 8, 28, 7, 35),
			ok:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseAbsoluteReset(tt.screen, 0, tt.now, tokyo)
			if ok != tt.ok {
				t.Fatalf("ParseAbsoluteReset() ok = %v, want %v (got %v)", ok, tt.ok, got)
			}
			if ok && !got.Equal(tt.want) {
				t.Errorf("ParseAbsoluteReset() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseAbsoluteReset_TailLinesRespected(t *testing.T) {
	screen := "⏺ You've hit your session limit · resets 8:10am (Asia/Tokyo)\n" +
		"a\nb\nc\nd\ne\nf\ng\nh"
	now := jst(2026, 8, 28, 7, 35)

	if _, ok := ParseAbsoluteReset(screen, 3, now, tokyo); ok {
		t.Error("走査範囲 (末尾 3 行) の外にある limit 行を読んでしまった")
	}
	if _, ok := ParseAbsoluteReset(screen, 0, now, tokyo); !ok {
		t.Error("n=0 (全行) なら読めるはず")
	}
}
