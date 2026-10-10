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

// clearMultiplexerEnv はテストを走らせる端末の多重化ツールの環境変数を空にする｡
// rex や herdr の中で go test を打つと継承した値が context file に混ざるため｡
func clearMultiplexerEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"REX_SESSION", "REX_BLOCK", "HERDR_PANE_ID"} {
		t.Setenv(key, "")
	}
}

func TestMultiplexerRefFromEnv(t *testing.T) {
	for _, tt := range []struct {
		name   string
		env    map[string]string
		want   MultiplexerRef
		wantOK bool
	}{
		{
			name:   "REX_BLOCK があれば rex",
			env:    map[string]string{"REX_SESSION": "session:01a1", "REX_BLOCK": "block:01a1", "HERDR_PANE_ID": "p1"},
			want:   MultiplexerRef{Kind: MultiplexerKindRex, Session: "session:01a1", Block: "block:01a1"},
			wantOK: true,
		},
		{
			name:   "REX_BLOCK が無く HERDR_PANE_ID があれば herdr",
			env:    map[string]string{"REX_SESSION": "session:01a1", "HERDR_PANE_ID": "p1"},
			want:   MultiplexerRef{Kind: MultiplexerKindHerdr, Pane: "p1"},
			wantOK: true,
		},
		{
			name: "どちらも無ければ無し",
			env:  map[string]string{"REX_SESSION": "session:01a1"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := MultiplexerRefFromEnv(envOf(tt.env))
			if ok != tt.wantOK || got != tt.want {
				t.Errorf("MultiplexerRefFromEnv() = %+v, %v, want %+v, %v", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestIngestStatuslineWithEnv_multiplexer(t *testing.T) {
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
				`,"multiplexer":{"kind":"rex","session":"session:01a1","block":"block:01a1"}}` + "\n",
		},
		{
			name: "herdr の pane を書く",
			env:  map[string]string{"HERDR_PANE_ID": "w1:p2"},
			want: `{"session_id":"sess-1","used_percentage":12,"observed_at":` + observed +
				`,"multiplexer":{"kind":"herdr","pane":"w1:p2"}}` + "\n",
		},
		{
			name: "多重化ツールの外なら multiplexer を書かない",
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

func TestIngestStatusline_環境変数からmultiplexerを読む(t *testing.T) {
	clearMultiplexerEnv(t)
	t.Setenv("REX_SESSION", "session:01a1")
	t.Setenv("REX_BLOCK", "block:01a1")
	s := New(t.TempDir())
	if err := s.IngestStatusline([]byte(inputNoRateLimits), ingestNow); err != nil {
		t.Fatalf("IngestStatusline() error = %v", err)
	}
	got, err := s.ListMultiplexerBlocks(MultiplexerKindRex)
	if err != nil {
		t.Fatalf("ListMultiplexerBlocks() error = %v", err)
	}
	if want := map[string]string{"block:01a1": "sess-1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("ListMultiplexerBlocks() = %v, want %v", got, want)
	}
}

func TestListMultiplexerBlocks(t *testing.T) {
	writeContext := func(t *testing.T, s Store, name, content string) {
		t.Helper()
		writeFile(t, filepath.Join(s.Context, name), content)
	}

	t.Run("kind ごとに block / pane から session_id を引く", func(t *testing.T) {
		s := New(t.TempDir())
		writeContext(t, s, "a.json", `{"session_id":"a","observed_at":10,"multiplexer":{"kind":"rex","session":"session:1","block":"block:1"}}`)
		writeContext(t, s, "b.json", `{"session_id":"b","observed_at":10,"multiplexer":{"kind":"herdr","pane":"p1"}}`)
		writeContext(t, s, "c.json", `{"session_id":"c","observed_at":10}`)

		rex, err := s.ListMultiplexerBlocks(MultiplexerKindRex)
		if err != nil {
			t.Fatalf("ListMultiplexerBlocks(rex) error = %v", err)
		}
		if want := map[string]string{"block:1": "a"}; !reflect.DeepEqual(rex, want) {
			t.Errorf("ListMultiplexerBlocks(rex) = %v, want %v", rex, want)
		}
		herdr, err := s.ListMultiplexerBlocks(MultiplexerKindHerdr)
		if err != nil {
			t.Fatalf("ListMultiplexerBlocks(herdr) error = %v", err)
		}
		if want := map[string]string{"p1": "b"}; !reflect.DeepEqual(herdr, want) {
			t.Errorf("ListMultiplexerBlocks(herdr) = %v, want %v", herdr, want)
		}
	})

	t.Run("同じ block の記録が複数あれば observed_at が最新のものを採る", func(t *testing.T) {
		// 同じ block で Claude Code を起動し直すと､古い session の記録が prune まで残る｡
		s := New(t.TempDir())
		writeContext(t, s, "old.json", `{"session_id":"old","observed_at":10,"multiplexer":{"kind":"rex","block":"block:1"}}`)
		writeContext(t, s, "new.json", `{"session_id":"new","observed_at":20,"multiplexer":{"kind":"rex","block":"block:1"}}`)
		writeContext(t, s, "zzz.json", `{"session_id":"zzz","observed_at":5,"multiplexer":{"kind":"rex","block":"block:1"}}`)

		got, err := s.ResolveMultiplexerBlock(MultiplexerKindRex, "block:1")
		if err != nil || got != "new" {
			t.Errorf("ResolveMultiplexerBlock() = %q, %v, want \"new\", nil", got, err)
		}
	})

	t.Run("observed_at が同じなら名前順で先のファイルを採る", func(t *testing.T) {
		s := New(t.TempDir())
		writeContext(t, s, "b.json", `{"session_id":"b","observed_at":10,"multiplexer":{"kind":"rex","block":"block:1"}}`)
		writeContext(t, s, "a.json", `{"session_id":"a","observed_at":10,"multiplexer":{"kind":"rex","block":"block:1"}}`)

		got, err := s.ResolveMultiplexerBlock(MultiplexerKindRex, "block:1")
		if err != nil || got != "a" {
			t.Errorf("ResolveMultiplexerBlock() = %q, %v, want \"a\", nil", got, err)
		}
	})

	t.Run("一致しなければ空", func(t *testing.T) {
		s := New(t.TempDir())
		writeContext(t, s, "a.json", `{"session_id":"a","observed_at":10,"multiplexer":{"kind":"rex","block":"block:1"}}`)

		got, err := s.ResolveMultiplexerBlock(MultiplexerKindRex, "block:2")
		if err != nil || got != "" {
			t.Errorf("ResolveMultiplexerBlock() = %q, %v, want \"\", nil", got, err)
		}
		// kind が違えば同じ ID でも一致しない
		got, err = s.ResolveMultiplexerBlock(MultiplexerKindHerdr, "block:1")
		if err != nil || got != "" {
			t.Errorf("ResolveMultiplexerBlock(herdr) = %q, %v, want \"\", nil", got, err)
		}
	})

	t.Run("壊れたファイル・session_id の無いファイル・json 以外は読み飛ばす", func(t *testing.T) {
		s := New(t.TempDir())
		writeContext(t, s, "broken.json", `{"session_id":"x","multiplexer":`)
		writeContext(t, s, "nosid.json", `{"observed_at":99,"multiplexer":{"kind":"rex","block":"block:1"}}`)
		writeContext(t, s, ".context.123", `{"session_id":"tmp","observed_at":99,"multiplexer":{"kind":"rex","block":"block:1"}}`)
		writeContext(t, s, "a.json", `{"session_id":"a","observed_at":10,"multiplexer":{"kind":"rex","block":"block:1"}}`)

		got, err := s.ListMultiplexerBlocks(MultiplexerKindRex)
		if err != nil {
			t.Fatalf("ListMultiplexerBlocks() error = %v", err)
		}
		if want := map[string]string{"block:1": "a"}; !reflect.DeepEqual(got, want) {
			t.Errorf("ListMultiplexerBlocks() = %v, want %v", got, want)
		}
	})

	t.Run("ディレクトリが無ければ空", func(t *testing.T) {
		s := New(t.TempDir())
		got, err := s.ListMultiplexerBlocks(MultiplexerKindRex)
		if err != nil || len(got) != 0 {
			t.Errorf("ListMultiplexerBlocks() = %v, %v, want empty, nil", got, err)
		}
	})

	t.Run("ゼロ値の Store は空", func(t *testing.T) {
		got, err := Store{}.ResolveMultiplexerBlock(MultiplexerKindRex, "block:1")
		if err != nil || got != "" {
			t.Errorf("ResolveMultiplexerBlock() = %q, %v, want \"\", nil", got, err)
		}
	})
}
