package dream

import (
	"bufio"
	"io"
)

// NewScanner は JSONL ファイル読み込み用の bufio.Scanner を作成する。Claude Code のセッション JSONL と dream が書く JSONL
// (harvest の record、mine の candidates) の読み手が共用する。
// バッファサイズは最大 10MB に設定される。
func NewScanner(r io.Reader) *bufio.Scanner {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
	return s
}
