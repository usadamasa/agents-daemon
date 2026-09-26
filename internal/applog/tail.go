package applog

import (
	"bufio"
	"errors"
	"fmt"
	"os"
)

// TailLogLines は path の末尾 n 行を返す｡ファイルが存在しない場合は
// (まだログが無い､あるいは daemon が動いていない) 空スライスを返す｡
func TailLogLines(path string, n int) ([]string, error) {
	f, err := os.Open(path) // #nosec G304 -- 呼び出し元が固定パスを渡す
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("ログファイルを開けません: %w", err)
	}
	defer func() { _ = f.Close() }()

	var lines []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
		if len(lines) > n {
			lines = lines[1:]
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("ログファイルの読み込みに失敗: %w", err)
	}
	return lines, nil
}
