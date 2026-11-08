# rex の全列挙のモデル (TLA+)

rex の全列挙 (`rexcli` の `snapshot`) と、その途中の session の close と server の停止の交錯を TLA+ に写したもの (#302)。
回し方と期待の突き合わせは `models/handoff/README.md` と同じ `task model-check`。

| cfg | 性質 | 成り立つ理由 |
| ---- | ---- | ---- |
| `PaneList.cfg` | `FailOnlyWhenDown`: 全列挙が失敗するのは server が落ちたときだけ | `block ls` の exit 3 は `ls` の後に閉じた session として読み飛ばす |
| | `SkipOnlyClosed`: 読み飛ばすのは閉じた session だけ | exit 3 になるのは閉じた session への `block ls` だけ |
| | `OkCoversListed`: 成功した全列挙は `ls` が返した session を全部打ち終えている | 読み飛ばしても残りの session へ進む |
| `PaneListNoSkip.cfg` | `FailOnlyWhenDown` が破れる (`violated` を期待) | exit 3 を全列挙の失敗にすると、session を閉じただけで失敗する。読み飛ばす根拠 |

同じ性質を実装に対して `rexcli/exec_test.go` が持つ。

| 性質 | テスト |
| ---- | ---- |
| `FailOnlyWhenDown` / `OkCoversListed` | `TestExecClient_PaneList_SkipsClosedSession` (閉じた session を飛ばして残りを打つ)、`TestExecClient_PaneList_FailureIsError` (exit 3 以外の失敗は全列挙の失敗) |

## 前提

- `ls` と `block ls` はそれぞれ 1 ステップ。
- 全列挙の途中で新しく作られた session は置かない。`ls` の後に作られた session は次の全列挙で拾う。
- `block call` の exit 3 (block の close) は置かない。session の exit 3 と同じ読み飛ばしで、失敗の条件を増やさない。
