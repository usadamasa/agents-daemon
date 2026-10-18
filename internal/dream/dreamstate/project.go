package dreamstate

import (
	"errors"
	"path/filepath"
	"strings"
)

// ResolveProject は dream サブコマンドの対象 project canonical name を解決する。
// 優先順位:
//  1. flag が canonical name 形式 (先頭が -) → そのまま返す
//  2. flag が絶対パス → canonicalize して返す
//  3. cwd が非空 → canonicalize
//  4. どちらもなければエラー
//
// canonical name の規則は CanonicalizeProjectPath を参照。
func ResolveProject(flag, cwd string) (string, error) {
	if flag != "" {
		if strings.HasPrefix(flag, "-") {
			return flag, nil
		}
		if filepath.IsAbs(flag) {
			return CanonicalizeProjectPath(flag), nil
		}
		return "", errors.New("--project は canonical name (先頭が -) か絶対パスで指定する")
	}
	if cwd == "" {
		return "", errors.New("--project 未指定かつ cwd も不明")
	}
	return CanonicalizeProjectPath(cwd), nil
}

// CanonicalizeProjectPath は絶対パスを Claude Code の project canonical name 形式に変換する。
// Claude Code は ~/.claude/projects/ 配下のディレクトリ名を作るとき、パス区切り (/)・
// アンダースコア (_)・ドット (.) をすべて - に置換する。
// 例: /Users/x_y/src/github.com/z → -Users-x-y-src-github-com-z
//
// harvest が記録する cwd_project と mine が解決する project name は文字列一致で
// 突き合わせるため、両 phase とも必ずこの関数を通す。規則の複製を作らないこと。
func CanonicalizeProjectPath(p string) string {
	if p == "" {
		return ""
	}
	return strings.NewReplacer("/", "-", "_", "-", ".", "-").Replace(p)
}
