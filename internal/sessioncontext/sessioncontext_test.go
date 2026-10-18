package sessioncontext

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"
)

// TestNew_ディレクトリ名が固定されている は protocol の固定。setup skill の確認手順と
// architecture.md がパスを直に書くので固定する。
func TestNew_ディレクトリ名が固定されている(t *testing.T) {
	if got, want := New("/state").Dir, "/state/context"; got != want {
		t.Errorf("Dir = %q, want %q", got, want)
	}
}

// envOf は map を getenv の形へ写す。無いキーは空文字列になる (os.Getenv と同じ)。
func envOf(m map[string]string) func(string) string {
	return func(key string) string { return m[key] }
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
			env:    map[string]string{"REX_SESSION": "session:01a1", "REX_BLOCK": "block:01a1"},
			want:   MultiplexerRef{Kind: MultiplexerKindRex, Session: "session:01a1", Block: "block:01a1"},
			wantOK: true,
		},
		{
			name: "REX_BLOCK が無ければ無し",
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

func TestWrite(t *testing.T) {
	observedAt := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	observed := strconv.FormatInt(observedAt.Unix(), 10)

	for _, tt := range []struct {
		name string
		mux  *MultiplexerRef
		want string
	}{
		{
			name: "rex の block と session を書く",
			mux:  &MultiplexerRef{Kind: MultiplexerKindRex, Session: "session:01a1", Block: "block:01a1"},
			want: `{"session_id":"sess-1","used_percentage":12,"observed_at":` + observed +
				`,"multiplexer":{"kind":"rex","session":"session:01a1","block":"block:01a1"}}` + "\n",
		},
		{
			name: "多重化ツールの外なら multiplexer を書かない",
			want: `{"session_id":"sess-1","used_percentage":12,"observed_at":` + observed + `}` + "\n",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := New(t.TempDir())
			if err := s.Write(Record{SessionID: "sess-1", UsedPercentage: 12, ObservedAt: observedAt, Multiplexer: tt.mux}); err != nil {
				t.Fatalf("Write() error = %v", err)
			}
			path, err := s.File("sess-1")
			if err != nil {
				t.Fatalf("File() error = %v", err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("ReadFile() error = %v", err)
			}
			if string(data) != tt.want {
				t.Errorf("context file = %q, want %q", data, tt.want)
			}
		})
	}

	t.Run("session ID が空か不正ならエラーで何も書かない", func(t *testing.T) {
		s := New(t.TempDir())
		for _, id := range []string{"", "../../etc/passwd"} {
			if err := s.Write(Record{SessionID: id, ObservedAt: observedAt}); err == nil {
				t.Errorf("Write(%q) がエラーを返さなかった", id)
			}
		}
		if _, err := os.Stat(s.Dir); !os.IsNotExist(err) {
			t.Errorf("context/ が作られている: %v", err)
		}
	})
}

func TestListMultiplexerBlocks(t *testing.T) {
	writeContext := func(t *testing.T, s Store, name, content string) {
		t.Helper()
		path := filepath.Join(s.Dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
	}

	t.Run("kind の block から session_id を引く (他の kind と multiplexer 無しは読み飛ばす)", func(t *testing.T) {
		s := New(t.TempDir())
		writeContext(t, s, "a.json", `{"session_id":"a","observed_at":10,"multiplexer":{"kind":"rex","session":"session:1","block":"block:1"}}`)
		writeContext(t, s, "b.json", `{"session_id":"b","observed_at":10,"multiplexer":{"kind":"tmux","block":"%1"}}`)
		writeContext(t, s, "c.json", `{"session_id":"c","observed_at":10}`)

		rex, err := s.ListMultiplexerBlocks(MultiplexerKindRex)
		if err != nil {
			t.Fatalf("ListMultiplexerBlocks(rex) error = %v", err)
		}
		if want := map[string]string{"block:1": "a"}; !reflect.DeepEqual(rex, want) {
			t.Errorf("ListMultiplexerBlocks(rex) = %v, want %v", rex, want)
		}
	})

	t.Run("同じ block の記録が複数あれば observed_at が最新のものを採る", func(t *testing.T) {
		// 同じ block で Claude Code を起動し直すと、古い session の記録が prune まで残る。
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
		got, err = s.ResolveMultiplexerBlock("tmux", "block:1")
		if err != nil || got != "" {
			t.Errorf("ResolveMultiplexerBlock(tmux) = %q, %v, want \"\", nil", got, err)
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
