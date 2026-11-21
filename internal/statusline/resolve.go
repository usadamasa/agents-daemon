package statusline

import (
	"os/exec"
	"strings"
)

// gitBranch は dir の branch 名を返す。detached なら "(detached)"、git 管理外か dir が空なら ""。
// branch を 1 fork で取り、空のときだけもう 1 fork でリポジトリかどうかを確かめる。
func gitBranch(dir string) string {
	if dir == "" {
		return ""
	}
	out, err := exec.Command("git", "-C", dir, "branch", "--show-current").Output() // #nosec G204 -- shell を通さず、dir は引数 1 つとして渡る
	if err == nil {
		if branch := strings.TrimSpace(string(out)); branch != "" {
			return branch
		}
	}
	if exec.Command("git", "-C", dir, "rev-parse", "--git-dir").Run() == nil { // #nosec G204 -- shell を通さず、dir は引数 1 つとして渡る
		return "(detached)"
	}
	return ""
}

// projectBranch は起動元プロジェクトの branch の表示。git 管理外でも 🏠 は出すので、
// 空なら "(detached)" で埋める。
func projectBranch(dir string) string {
	if branch := gitBranch(dir); branch != "" {
		return branch
	}
	return "(detached)"
}

// outsideProject は cwd が起動元 project_dir の外 (別 dir か別プロジェクト) にあるかを返す。
// 前方一致ではなく区切りまで見る (/tmp/foo と /tmp/foobar を区別する)。どちらかが空なら false。
func outsideProject(cur, project string) bool {
	if cur == "" || project == "" || cur == project {
		return false
	}
	return !strings.HasPrefix(cur, project+"/")
}
