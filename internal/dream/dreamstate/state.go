// Package dreamstate は dream サブコマンド共通の state ディレクトリの導出と掃除、date / project の解決を提供する。
//
// 環境変数を読む入口は apppath.FromEnv に限り、このパッケージは XDG を見ない。各サブコマンドは cobra 層で
// Dir(apppath.Paths.StateDir()) と解決した絶対パスを Opts で受け取る。
package dreamstate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Dir は XDG state ディレクトリ (apppath.Paths.StateDir) から dream の state の置き場を導出する。
// 配下は harvest/、harvest-state.json、candidates/ (バイナリが書く) と replays/、verdicts/ (dream skill が書く)。
// cost/ を spend が導出するのと同じ形で、書き手が自分の置き場を決める。
func Dir(stateDir string) string {
	return filepath.Join(stateDir, "dream")
}

// ResolveDate は explicit が空なら今日の YYYY-MM-DD を返す。
// テストで固定化したい場合は explicit を渡す。
func ResolveDate(explicit string) string {
	if explicit != "" {
		return explicit
	}
	return time.Now().Format("2006-01-02")
}

// retention は dream の state (harvest の jsonl、candidates、skill が書く replays / verdicts) を残す期間。
// mine は harvest ディレクトリの全日付を読むので、`harvest --days` の既定 (14 日) より長く、
// dream を数か月放置しても直近の判断材料が残る 90 日にする。これより古い候補は再 mine で作り直せる。
const retention = 90 * 24 * time.Hour

// pruneDirs は Prune が掃除する Dir 直下のサブディレクトリ。
// harvest-state.json は直下にあって毎回書き直されるので対象外。
var pruneDirs = []string{"harvest", "candidates", "replays", "verdicts"}

// Prune は dreamDir 配下の各サブディレクトリから、mtime が retention を過ぎた通常ファイルを消し、
// 消した数を返す。無いディレクトリは飛ばす。`dream harvest` が書き込みの前に呼ぶ
// (daemon の housekeeping は rex の外では走らないため、書き手が消す)。
func Prune(dreamDir string, now time.Time) (int, error) {
	removed := 0
	var errs []error
	for _, name := range pruneDirs {
		n, err := pruneDir(filepath.Join(dreamDir, name), now.Add(-retention))
		removed += n
		if err != nil {
			errs = append(errs, err)
		}
	}
	return removed, errors.Join(errs...)
}

// pruneDir は dir 直下の通常ファイルのうち mtime が cutoff より古いものを消す。サブディレクトリは触らない。
func pruneDir(dir string, cutoff time.Time) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, fmt.Errorf("%s の走査に失敗: %w", dir, err)
	}
	removed := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return removed, fmt.Errorf("%s の情報を取得できません: %w", entry.Name(), err)
		}
		if !info.ModTime().Before(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil {
			return removed, fmt.Errorf("%s の削除に失敗: %w", entry.Name(), err)
		}
		removed++
	}
	return removed, nil
}
