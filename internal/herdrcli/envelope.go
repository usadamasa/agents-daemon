package herdrcli

import (
	"encoding/json"
	"fmt"
	"strings"
)

// envelope は `herdr pane <subcommand>` が返す JSON 応答の共通形。
// result の中身はサブコマンドごとに異なるため json.RawMessage で保持し、
// 呼び出し側が個別に Unmarshal する。
//
// 注意: これは pane 系サブコマンドの形であり、`herdr status --json` のような
// トップレベルコマンドは envelope に包まれない生の JSON を返す。混同しない。
type envelope struct {
	ID     string          `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *envelopeError  `json:"error"`
}

// envelopeError は envelope.error の中身。herdr は pane_not_found のような
// 既知のエラーでも exit code 1 とともにこの形の JSON を吐く。
type envelopeError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *envelopeError) Error() string {
	return fmt.Sprintf("herdr: %s (%s)", e.Message, e.Code)
}

// parseEnvelope は herdr CLI の stdout/stderr から JSON envelope を取り出す。
//
// herdr は基本的に stdout へ 1 行の JSON envelope だけを出すが、以下の揺れを
// 許容する必要がある (参考実装 herdr-claude-auto-retry の herdr.js, parseEnvelope
// のロジックを踏襲):
//   - stdout の前後にログや警告行が混ざる
//   - envelope 自体が stdout ではなく stderr に出る
//
// このため stdout → stderr の順に、まず全体を JSON として parse を試み、
// 失敗したら末尾から "{" で始まる行を探して parse する、という 2 段構えで走査する。
func parseEnvelope(stdout, stderr []byte) (*envelope, error) {
	if env := tryParseEnvelope(stdout); env != nil {
		return env, nil
	}
	if env := tryParseEnvelope(stderr); env != nil {
		return env, nil
	}
	return nil, fmt.Errorf("herdr: 応答から JSON envelope を取り出せなかった (stdout=%q, stderr=%q)", stdout, stderr)
}

func tryParseEnvelope(out []byte) *envelope {
	s := strings.TrimSpace(string(out))
	if s == "" {
		return nil
	}

	var env envelope
	if err := json.Unmarshal([]byte(s), &env); err == nil {
		return &env
	}

	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, "{") {
			continue
		}
		if err := json.Unmarshal([]byte(line), &env); err == nil {
			return &env
		}
	}
	return nil
}
