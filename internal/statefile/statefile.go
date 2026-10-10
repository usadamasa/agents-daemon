// Package statefile は XDG state 配下に別プロセスが読み書きするファイルの共通の作法を持つ｡
// セッション ID をファイル名に使うときの検証と､書きかけを読ませないための原子的な書き込み｡
// sessionstate (daemon が読む sidecar) と spend (statusline の cost の記録) が使う｡
package statefile

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// sessionIDPattern はファイル名に使ってよい session ID の形｡多重化ツールや入力から受け取った
// 値をそのままパスへ結合するため､区切り文字を含む値でディレクトリの外へ
// 出られないことをここで保証する｡
var sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// ValidSessionID は id をファイル名に使ってよいかを返す｡空は不可｡
func ValidSessionID(id string) bool {
	return sessionIDPattern.MatchString(id)
}

// WriteJSONAtomic は v を JSON 1 行 (末尾に改行) にして path へ原子的に書く｡
func WriteJSONAtomic(path, tmpPattern string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("%s の JSON 化に失敗: %w", filepath.Base(path), err)
	}
	return WriteFileAtomic(path, tmpPattern, append(data, '\n'))
}

// WriteFileAtomic は data を path へ原子的に書く｡
// 一時ファイルは path と同じディレクトリに tmpPattern の名前で作る｡別のファイル
// システムだと rename が原子的でなくなり､読み手が書きかけを読むため｡rename 前に
// 失敗したら一時ファイルを消す (消し損ねても housekeeping が mtime で拾う)｡
func WriteFileAtomic(path, tmpPattern string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("%s の作成に失敗: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, tmpPattern)
	if err != nil {
		return fmt.Errorf("一時ファイルの作成に失敗: %w", err)
	}
	if err := writeAndClose(tmp, data); err != nil {
		return errors.Join(err, os.Remove(tmp.Name()))
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return errors.Join(fmt.Errorf("%s への置き換えに失敗: %w", path, err), os.Remove(tmp.Name()))
	}
	return nil
}

// writeAndClose は f へ data を書き切って閉じる｡Close の失敗も書き込みの失敗として返す｡
func writeAndClose(f *os.File, data []byte) error {
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("一時ファイルへの書き込みに失敗: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("一時ファイルのクローズに失敗: %w", err)
	}
	return nil
}
