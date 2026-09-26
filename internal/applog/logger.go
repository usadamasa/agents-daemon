// Package applog は agents-daemon のログファイルの書き込み､日付境界での
// rotate､古い rotate 済みファイルの削除､末尾の読み出しを提供する｡
package applog

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// dayLayout は rotate 済みファイル名に後置する日付の書式｡
const dayLayout = "2006-01-02"

// RotatedPath は現行ログ current を day の分として退避するときのパス
// (<current>.YYYY-MM-DD) を返す｡
func RotatedPath(current, day string) string {
	return current + "." + day
}

// Logger は固定名のログファイル (daemon.log) へ追記し､日付が変わったら
// 書き込みの直前に旧ファイルを <path>.YYYY-MM-DD へ退避してから新しいファイルへ
// 書き始める｡ファイル名を固定にしておくと tail -f や `logs` サブコマンドの
// 参照先が日付で変わらない｡
type Logger struct {
	path string
	now  func() time.Time
	day  string // 開いているファイルの最終書き込み日 (rotate の判定に使う)
	file *os.File
}

// NewLogger は path へ書く Logger を作る｡now はテストが日付境界を差し替え
// られるように注入する (本番では time.Now を渡す)｡
func NewLogger(path string, now func() time.Time) *Logger {
	return &Logger{path: path, now: now}
}

// ensureOpen は当日分として書けるファイルが開いていることを保証する｡
//
// 開いているファイルの最終書き込み日が今日と違えば rotate する｡初回は
// ファイルの mtime から最終書き込み日を取るので､daemon の再起動を跨いで
// 日付が変わっていた場合も前プロセスの残したファイルを退避できる｡
func (l *Logger) ensureOpen() error {
	today := l.now().Format(dayLayout)
	if l.file != nil && l.day == today {
		return nil
	}
	if l.file == nil {
		if err := l.adoptExistingDay(); err != nil {
			return err
		}
	}
	if l.file != nil && l.day != today {
		if err := l.rotate(); err != nil {
			return err
		}
	}
	if l.file == nil {
		if err := l.open(); err != nil {
			return err
		}
	}
	l.day = today
	return nil
}

// adoptExistingDay は既存ファイルがあればそれを開き､mtime の日付を l.day に
// 取り込む｡無ければ何もしない (open が作る)｡
func (l *Logger) adoptExistingDay() error {
	fi, err := os.Stat(l.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("ログファイルの情報を取得できません: %w", err)
	}
	if err := l.open(); err != nil {
		return err
	}
	l.day = fi.ModTime().In(l.now().Location()).Format(dayLayout)
	return nil
}

// open はログファイルを追記モードで開く (無ければ作る)｡
func (l *Logger) open() error {
	if err := os.MkdirAll(filepath.Dir(l.path), 0o700); err != nil {
		return fmt.Errorf("ログディレクトリの作成に失敗: %w", err)
	}
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600) // #nosec G304 -- 呼び出し元が固定パスを渡す
	if err != nil {
		return fmt.Errorf("ログファイルを開けません: %w", err)
	}
	l.file = f
	return nil
}

// rotate は開いているファイルを閉じて l.day の名前へ退避する｡退避先が既に
// あれば上書きせず､そのまま現行ファイルへ追記し続ける (次の日付境界で
// 改めて試みる)｡同名が生じるのは時計が巻き戻ったときくらいで､その状況で
// 既存の記録を消すより､行が混ざる方がまし｡
func (l *Logger) rotate() error {
	target := RotatedPath(l.path, l.day)
	if _, err := os.Stat(target); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("退避先の情報を取得できません: %w", err)
	}
	_ = l.file.Close()
	l.file = nil
	if err := os.Rename(l.path, target); err != nil {
		return fmt.Errorf("ログファイルの退避に失敗: %w", err)
	}
	return nil
}

// Logf はタイムスタンプ付きの 1 行をログへ書く｡ログ出力自体の失敗で daemon
// 本体を止めないよう､エラーは戻さず無視する (最悪ログが欠けるだけに留める)｡
func (l *Logger) Logf(format string, args ...any) {
	if err := l.ensureOpen(); err != nil {
		return
	}
	ts := l.now().Format(time.RFC3339)
	// ログ出力の失敗はログにも書けないので､ここで扱える手が無い｡
	_, _ = fmt.Fprintf(l.file, "%s "+format+"\n", append([]any{ts}, args...)...)
}

// Close は開いているログファイルを閉じる｡
func (l *Logger) Close() error {
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}

// PruneRotated は current の rotate 済みファイル (<current>.YYYY-MM-DD) のうち､
// mtime が now から maxAge より古いものを削除し､消した数を返す｡現行ファイルと
// 命名規則に合わないファイルは触らない｡ディレクトリが無ければ (0, nil)｡
func PruneRotated(current string, now time.Time, maxAge time.Duration) (int, error) {
	dir := filepath.Dir(current)
	prefix := filepath.Base(current) + "."
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, fmt.Errorf("ログディレクトリの走査に失敗: %w", err)
	}

	cutoff := now.Add(-maxAge)
	removed := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !isRotatedName(name, prefix) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return removed, fmt.Errorf("%s の情報を取得できません: %w", name, err)
		}
		if !info.ModTime().Before(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			return removed, fmt.Errorf("%s の削除に失敗: %w", name, err)
		}
		removed++
	}
	return removed, nil
}

// isRotatedName は name が rotate 済みファイルの形 (<prefix>YYYY-MM-DD) かを返す｡
// 接頭辞が同じでも日付でない後置 (daemon.log.bak 等) は対象にしない｡
func isRotatedName(name, prefix string) bool {
	if !strings.HasPrefix(name, prefix) {
		return false
	}
	_, err := time.Parse(dayLayout, strings.TrimPrefix(name, prefix))
	return err == nil
}
