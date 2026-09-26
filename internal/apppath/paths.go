// Package apppath は daemon / CLI が触るランタイムファイルの絶対パスを解決する。
//
// 設定とランタイム状態で置き場所を分ける。どちらも XDG Base Directory に従い、
// ハーネスが管理する ~/.claude/ には置かない。設定 (config.json) は利用者が書く
// ものなので config ($XDG_CONFIG_HOME、無ければ ~/.config) 配下に置く。daemon が
// 書くランタイム状態 (PID / status / logs / rate-limits) は揮発物なので state
// ($XDG_STATE_HOME、無ければ ~/.local/state) 配下に置く。
package apppath

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// appName は XDG config / state 配下でこのツールが占めるディレクトリ名。
const appName = "agents-daemon"

// Paths は XDG config / state の起点から設定とランタイムファイルの絶対パスを解決する。
// 起点を明示的に受け取ることで、テストは $HOME / $XDG_* 相当を
// 一時ディレクトリへ差し替えられる (実行時は FromEnv が環境から埋める)。
type Paths struct {
	configDir string
	stateDir  string
}

// New は home と xdgStateHome ($XDG_STATE_HOME の値。空なら ~/.local/state)、
// xdgConfigHome ($XDG_CONFIG_HOME の値。空なら ~/.config) を起点にした Paths を作る。
// 空白だけの値は未設定として扱う。
func New(home, xdgStateHome, xdgConfigHome string) Paths {
	return Paths{
		configDir: filepath.Join(xdgBase(xdgConfigHome, home, ".config"), appName),
		stateDir:  filepath.Join(xdgBase(xdgStateHome, home, ".local", "state"), appName),
	}
}

// xdgBase は XDG 変数の値が空 (空白だけを含む) なら home 配下の既定値を返す。
func xdgBase(value, home string, fallback ...string) string {
	if base := strings.TrimSpace(value); base != "" {
		return base
	}
	return filepath.Join(append([]string{home}, fallback...)...)
}

// FromEnv は os.UserHomeDir と $XDG_STATE_HOME / $XDG_CONFIG_HOME から Paths を作る。
func FromEnv() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, fmt.Errorf("ホームディレクトリの取得に失敗: %w", err)
	}
	return New(home, os.Getenv("XDG_STATE_HOME"), os.Getenv("XDG_CONFIG_HOME")), nil
}

// ConfigFile は設定ファイル (<XDG config>/agents-daemon/config.json) のパス。
// 無くてもよく、その場合は既定値で動く (config.Load)。
func (p Paths) ConfigFile() string {
	return filepath.Join(p.configDir, "config.json")
}

// StateDir はランタイム状態 (PID / status / rate-limits / logs) を置くディレクトリ。
// 構成管理の対象外で、daemon が必要に応じて作成する。
func (p Paths) StateDir() string {
	return p.stateDir
}

// 別プロセスが書く sidecar file (rate-limits / context / compact-state / compacted)
// のディレクトリとファイル名は sessionstate が StateDir から導出する。読み込み側と
// 同じ場所に置いて、書き出し先の規則が二重にならないようにするため。

// PIDFile は稼働中 daemon の PID を保持するファイルのパス。
func (p Paths) PIDFile() string {
	return filepath.Join(p.StateDir(), "daemon.pid")
}

// StatusFile は daemon が毎 tick 書き出す状態スナップショットのパス。
func (p Paths) StatusFile() string {
	return filepath.Join(p.StateDir(), "status.json")
}

// LogFile は現行ログのパス (logs/daemon.log)。名前は固定で、日付が変わると applog が
// このパスに旧日付を後置した名前 (daemon.log.YYYY-MM-DD) へ同じディレクトリ内で退避する。
// ディレクトリだけが要る側は filepath.Dir で取る (LogFile と別に公開するほどの意味が無い)。
func (p Paths) LogFile() string {
	return filepath.Join(p.StateDir(), "logs", "daemon.log")
}
