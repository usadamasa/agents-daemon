package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// 開いたまま新しいバージョンへ切り替える。hooks/build-gui.sh は .app を入れ替えてから stamp を書くので、stamp が
// 自分の version より新しければ新しい .app が揃っている。古い側は窓と画面の状態を restore.json に書き、新しい側を
// 前面を奪わずに起こしてから終わる。新しい側は起動時に restore.json を読んで戻し、読んだかどうかによらず消す。

// upgradeEvent は切り替えられるようになったことを frontend へ知らせる event の名前。
const upgradeEvent = "agent-tasks:upgrade"

// upgradePoll は stamp を読み直す間隔。build は SessionStart のたびに走るが、走るのは plugin の更新後だけ。
const upgradePoll = 5 * time.Second

// restoreMaxAge より古い restore.json は読まずに捨てる。新しい側は数秒で起きるので、これを越えたものは起動に
// 失敗した残りで、次に手で開いたときに拾わない。
const restoreMaxAge = 2 * time.Minute

// restoreState は切り替えの前後で受け渡す窓と画面の状態。View は frontend の restore.js が鍵ごとに詰める。
type restoreState struct {
	Version string                     `json:"version"`
	SavedAt time.Time                  `json:"saved_at"`
	Bounds  application.Rect           `json:"bounds"`
	View    map[string]json.RawMessage `json:"view"`
}

// restorePath は restore.json の置き場。ランタイムの状態なので XDG_STATE_HOME の下に置く。
func restorePath() string {
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" {
		state = filepath.Join(os.Getenv("HOME"), ".local", "state")
	}
	return filepath.Join(state, "agents-daemon", "gui", "restore.json")
}

// stampPath と appPath は hooks/lib/daemon-bin.sh の GUI_STAMP と GUI_APP と同じ置き場。
func stampPath() string {
	return filepath.Join(filepath.Dir(daemonBin()), "agent-tasks.version")
}

func appPath() string {
	return filepath.Join(os.Getenv("HOME"), "Applications", "agent-tasks.app")
}

// readStamp は stamp の version を返す。読めなければ空。
func readStamp(path string) string {
	b, err := os.ReadFile(path) // #nosec G304 -- 置き場は固定 (stampPath)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// newer は stamp が own より新しいか。欄ごとに 10 進の数として比べ、欄の数が違えば足りない欄を 0 とする
// (hooks/lib/daemon-bin.sh の version_older と同じ)。どちらかが `.` 区切りの数でなければ偽 (dev は切り替えない)。
func newer(own, stamp string) bool {
	a, okA := versionFields(own)
	b, okB := versionFields(stamp)
	if !okA || !okB {
		return false
	}
	for i := range max(len(a), len(b)) {
		x, y := field(a, i), field(b, i)
		if x != y {
			return x < y
		}
	}
	return false
}

func versionFields(v string) ([]int, bool) {
	if v == "" {
		return nil, false
	}
	parts := strings.Split(v, ".")
	out := make([]int, len(parts))
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || strings.ContainsAny(p, "+-") {
			return nil, false
		}
		out[i] = n
	}
	return out, true
}

func field(f []int, i int) int {
	if i < len(f) {
		return f[i]
	}
	return 0
}

// saveRestore は restore.json を一時ファイルから rename で置く (新しい側が書きかけを読まない)。
func saveRestore(path string, s restoreState) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".restore.*.json")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}

// loadRestore は version が合い、now から restoreMaxAge 以内に書かれた restore.json を返す。ファイルは読めたかに
// よらず消す。
func loadRestore(path, version string, now time.Time) (restoreState, bool) {
	b, err := os.ReadFile(path) // #nosec G304 -- 置き場は固定 (restorePath)
	if err != nil {
		return restoreState{}, false
	}
	_ = os.Remove(path)
	var s restoreState
	if err := json.Unmarshal(b, &s); err != nil {
		return restoreState{}, false
	}
	age := now.Sub(s.SavedAt)
	if s.Version != version || age > restoreMaxAge || age < -restoreMaxAge {
		return restoreState{}, false
	}
	return s, true
}

// upgrader は切り替えの手順。launch と now はテストで差し替える。
type upgrader struct {
	version string
	stamp   string
	restore string
	app     string
	launch  func(name string, args ...string) error
	now     func() time.Time
}

func newUpgrader() *upgrader {
	return &upgrader{
		version: version,
		stamp:   stampPath(),
		restore: restorePath(),
		app:     appPath(),
		launch: func(name string, args ...string) error {
			return exec.Command(name, args...).Run() // #nosec G204 -- shell を通さず、name と args はコードの定数と固定の置き場
		},
		now: time.Now,
	}
}

// ready は新しいバージョンの .app が揃っているか。
func (u *upgrader) ready() bool {
	return newer(u.version, readStamp(u.stamp))
}

// switchTo は bounds と view (restore.js が詰めた JSON の object) を新しいバージョン宛てに書き、新しい .app を前面を
// 奪わずに起こす。起こせなければ restore.json を消して err を返す (古い側は動き続ける)。
func (u *upgrader) switchTo(bounds application.Rect, view string) error {
	target := readStamp(u.stamp)
	if !newer(u.version, target) {
		return fmt.Errorf("新しいバージョンが無い (いま %s、stamp %q)", u.version, target)
	}
	var v map[string]json.RawMessage
	if err := json.Unmarshal([]byte(view), &v); err != nil {
		return fmt.Errorf("画面の状態を読めない: %w", err)
	}
	s := restoreState{Version: target, SavedAt: u.now(), Bounds: bounds, View: v}
	if err := saveRestore(u.restore, s); err != nil {
		return fmt.Errorf("restore を書けない: %w", err)
	}
	// -g は前面を奪わない、-n は起動中の自分とは別に新しいプロセスを起こす。
	if err := u.launch("open", "-g", "-n", u.app); err != nil {
		if rmErr := os.Remove(u.restore); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			err = errors.Join(err, rmErr)
		}
		return fmt.Errorf("%s を起こせない: %w", u.app, err)
	}
	return nil
}

// watch は stamp を読み直し、新しいバージョンが揃っている間は毎回 frontend へ知らせる (切り替えに失敗しても次の
// 知らせでまた試す)。
func (u *upgrader) watch(app *application.App) {
	t := time.NewTicker(upgradePoll)
	defer t.Stop()
	for range t.C {
		if u.ready() {
			app.Event.Emit(upgradeEvent)
		}
	}
}
