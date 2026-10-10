package sessionstate

import (
	"path/filepath"
	"reflect"
	"testing"
)

// envOf は map を getenv の形へ写す｡無いキーは空文字列になる (os.Getenv と同じ)｡
func envOf(m map[string]string) func(string) string {
	return func(key string) string { return m[key] }
}

// clearMuxEnv はテストを走らせる端末の多重化ツールの環境変数を空にする｡
// rex や herdr の中で go test を打つと継承した値が context file に混ざるため｡
func clearMuxEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"REX_SESSION", "REX_BLOCK", "HERDR_PANE_ID"} {
		t.Setenv(key, "")
	}
}

func TestMuxRefFromEnv(t *testing.T) {
	for _, tt := range []struct {
		name   string
		env    map[string]string
		want   MuxRef
		wantOK bool
	}{
		{
			name:   "REX_BLOCK があれば rex",
			env:    map[string]string{"REX_SESSION": "session:01a1", "REX_BLOCK": "block:01a1", "HERDR_PANE_ID": "p1"},
			want:   MuxRef{Kind: MuxKindRex, Session: "session:01a1", Block: "block:01a1"},
			wantOK: true,
		},
		{
			name:   "REX_BLOCK が無く HERDR_PANE_ID があれば herdr",
			env:    map[string]string{"REX_SESSION": "session:01a1", "HERDR_PANE_ID": "p1"},
			want:   MuxRef{Kind: MuxKindHerdr, Pane: "p1"},
			wantOK: true,
		},
		{
			name: "どちらも無ければ無し",
			env:  map[string]string{"REX_SESSION": "session:01a1"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := MuxRefFromEnv(envOf(tt.env))
			if ok != tt.wantOK || got != tt.want {
				t.Errorf("MuxRefFromEnv() = %+v, %v, want %+v, %v", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestIngestStatuslineWithEnv_mux(t *testing.T) {
	observed := itoa(ingestNow.Unix())
	for _, tt := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{
			name: "rex の block と session を書く",
			env:  map[string]string{"REX_SESSION": "session:01a1", "REX_BLOCK": "block:01a1"},
			want: `{"session_id":"sess-1","used_percentage":12,"observed_at":` + observed +
				`,"mux":{"kind":"rex","session":"session:01a1","block":"block:01a1"}}` + "\n",
		},
		{
			name: "herdr の pane を書く",
			env:  map[string]string{"HERDR_PANE_ID": "w1:p2"},
			want: `{"session_id":"sess-1","used_percentage":12,"observed_at":` + observed +
				`,"mux":{"kind":"herdr","pane":"w1:p2"}}` + "\n",
		},
		{
			name: "多重化ツールの外なら mux を書かない",
			env:  map[string]string{},
			want: `{"session_id":"sess-1","used_percentage":12,"observed_at":` + observed + `}` + "\n",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := New(t.TempDir())
			if err := s.IngestStatuslineWithEnv([]byte(inputNoRateLimits), envOf(tt.env), ingestNow); err != nil {
				t.Fatalf("IngestStatuslineWithEnv() error = %v", err)
			}
			if got := readContextRaw(t, s, "sess-1"); got != tt.want {
				t.Errorf("context file = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIngestStatusline_環境変数からmuxを読む(t *testing.T) {
	clearMuxEnv(t)
	t.Setenv("REX_SESSION", "session:01a1")
	t.Setenv("REX_BLOCK", "block:01a1")
	s := New(t.TempDir())
	if err := s.IngestStatusline([]byte(inputNoRateLimits), ingestNow); err != nil {
		t.Fatalf("IngestStatusline() error = %v", err)
	}
	got, err := s.ListMuxBlocks(MuxKindRex)
	if err != nil {
		t.Fatalf("ListMuxBlocks() error = %v", err)
	}
	if want := map[string]string{"block:01a1": "sess-1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("ListMuxBlocks() = %v, want %v", got, want)
	}
}

func TestListMuxBlocks(t *testing.T) {
	writeContext := func(t *testing.T, s Store, name, content string) {
		t.Helper()
		writeFile(t, filepath.Join(s.Context, name), content)
	}

	t.Run("kind ごとに block / pane から session_id を引く", func(t *testing.T) {
		s := New(t.TempDir())
		writeContext(t, s, "a.json", `{"session_id":"a","observed_at":10,"mux":{"kind":"rex","session":"session:1","block":"block:1"}}`)
		writeContext(t, s, "b.json", `{"session_id":"b","observed_at":10,"mux":{"kind":"herdr","pane":"p1"}}`)
		writeContext(t, s, "c.json", `{"session_id":"c","observed_at":10}`)

		rex, err := s.ListMuxBlocks(MuxKindRex)
		if err != nil {
			t.Fatalf("ListMuxBlocks(rex) error = %v", err)
		}
		if want := map[string]string{"block:1": "a"}; !reflect.DeepEqual(rex, want) {
			t.Errorf("ListMuxBlocks(rex) = %v, want %v", rex, want)
		}
		herdr, err := s.ListMuxBlocks(MuxKindHerdr)
		if err != nil {
			t.Fatalf("ListMuxBlocks(herdr) error = %v", err)
		}
		if want := map[string]string{"p1": "b"}; !reflect.DeepEqual(herdr, want) {
			t.Errorf("ListMuxBlocks(herdr) = %v, want %v", herdr, want)
		}
	})

	t.Run("同じ block の記録が複数あれば observed_at が最新のものを採る", func(t *testing.T) {
		// 同じ block で Claude Code を起動し直すと､古い session の記録が prune まで残る｡
		s := New(t.TempDir())
		writeContext(t, s, "old.json", `{"session_id":"old","observed_at":10,"mux":{"kind":"rex","block":"block:1"}}`)
		writeContext(t, s, "new.json", `{"session_id":"new","observed_at":20,"mux":{"kind":"rex","block":"block:1"}}`)
		writeContext(t, s, "zzz.json", `{"session_id":"zzz","observed_at":5,"mux":{"kind":"rex","block":"block:1"}}`)

		got, err := s.ResolveMuxBlock(MuxKindRex, "block:1")
		if err != nil || got != "new" {
			t.Errorf("ResolveMuxBlock() = %q, %v, want \"new\", nil", got, err)
		}
	})

	t.Run("observed_at が同じなら名前順で先のファイルを採る", func(t *testing.T) {
		s := New(t.TempDir())
		writeContext(t, s, "b.json", `{"session_id":"b","observed_at":10,"mux":{"kind":"rex","block":"block:1"}}`)
		writeContext(t, s, "a.json", `{"session_id":"a","observed_at":10,"mux":{"kind":"rex","block":"block:1"}}`)

		got, err := s.ResolveMuxBlock(MuxKindRex, "block:1")
		if err != nil || got != "a" {
			t.Errorf("ResolveMuxBlock() = %q, %v, want \"a\", nil", got, err)
		}
	})

	t.Run("一致しなければ空", func(t *testing.T) {
		s := New(t.TempDir())
		writeContext(t, s, "a.json", `{"session_id":"a","observed_at":10,"mux":{"kind":"rex","block":"block:1"}}`)

		got, err := s.ResolveMuxBlock(MuxKindRex, "block:2")
		if err != nil || got != "" {
			t.Errorf("ResolveMuxBlock() = %q, %v, want \"\", nil", got, err)
		}
		// kind が違えば同じ ID でも一致しない
		got, err = s.ResolveMuxBlock(MuxKindHerdr, "block:1")
		if err != nil || got != "" {
			t.Errorf("ResolveMuxBlock(herdr) = %q, %v, want \"\", nil", got, err)
		}
	})

	t.Run("壊れたファイル・session_id の無いファイル・json 以外は読み飛ばす", func(t *testing.T) {
		s := New(t.TempDir())
		writeContext(t, s, "broken.json", `{"session_id":"x","mux":`)
		writeContext(t, s, "nosid.json", `{"observed_at":99,"mux":{"kind":"rex","block":"block:1"}}`)
		writeContext(t, s, ".context.123", `{"session_id":"tmp","observed_at":99,"mux":{"kind":"rex","block":"block:1"}}`)
		writeContext(t, s, "a.json", `{"session_id":"a","observed_at":10,"mux":{"kind":"rex","block":"block:1"}}`)

		got, err := s.ListMuxBlocks(MuxKindRex)
		if err != nil {
			t.Fatalf("ListMuxBlocks() error = %v", err)
		}
		if want := map[string]string{"block:1": "a"}; !reflect.DeepEqual(got, want) {
			t.Errorf("ListMuxBlocks() = %v, want %v", got, want)
		}
	})

	t.Run("ディレクトリが無ければ空", func(t *testing.T) {
		s := New(t.TempDir())
		got, err := s.ListMuxBlocks(MuxKindRex)
		if err != nil || len(got) != 0 {
			t.Errorf("ListMuxBlocks() = %v, %v, want empty, nil", got, err)
		}
	})

	t.Run("ゼロ値の Store は空", func(t *testing.T) {
		got, err := Store{}.ResolveMuxBlock(MuxKindRex, "block:1")
		if err != nil || got != "" {
			t.Errorf("ResolveMuxBlock() = %q, %v, want \"\", nil", got, err)
		}
	})
}
