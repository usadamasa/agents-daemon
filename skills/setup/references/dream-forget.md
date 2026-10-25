# dream forget を sandbox の外で動かす

`agents-daemon dream forget` は auto memory (`~/.claude/projects/<project>/memory/*.md`) を消す。
このディレクトリは Claude Code 組み込みの sandbox 保護対象で、`allowWrite` で再許可する表現が無いため、
sandbox の中では `operation not permitted` になる。`dream forget` だけを `sandbox.excludedCommands` で外に出す。
`rm` を出さない理由と削除範囲の制約は `agents-daemon:dream` skill の `references/layer-auto-memory.md`。

<!-- cc-premise: 2.1.310 sandbox excludedCommands は Bash 許可ルールと同じ構文で呼び出しの文字列に当たる。変数から来る command name は sandbox に残る -->

## 足す行

利用者の `~/.claude/settings.json`。settings を生成している環境では、その原本 (agents-config なら
`dotclaude/settings/base.yaml`) に足す。生成物に手で足しても次の生成で消える。

```json
{
  "sandbox": {
    "excludedCommands": ["~/.cache/agents-daemon/bin/agents-daemon dream forget *"]
  }
}
```

`XDG_CACHE_HOME` を設定している環境では、`~/.cache` をその値に読み替える (呼ぶ側のコマンドも同じ)。

## 照合の規則

- 要素は `Bash(...)` の許可ルールと同じ構文で、`*` は任意の文字列に当たる。呼び出しの文字列そのものと照合するので、
  basename だけの `agents-daemon dream forget *` は絶対パスで呼んだコマンドに当たらない
- 先頭は `~/.cache/` で固定し、`*` は末尾にだけ置く。`*/agents-daemon/bin/agents-daemon dream forget *` のように
  先頭を `*` にすると、cwd の下に作った偽の `x/agents-daemon/bin/agents-daemon` にも当たり、sandbox の外で動く
- 呼ぶ側は `~/.cache/agents-daemon/bin/agents-daemon dream forget <name>` と literal のパスで打つ。
  `"${XDG_CACHE_HOME:-$HOME/.cache}/..."` のように command name が変数から来る呼び出しは、要素に当たっても
  sandbox に残る。`cd` や `$(...)` や `>` を同じ呼び出しに混ぜても同じ
- 除外しても permission flow は通る (auto mode なら classifier、Manual なら prompt)

## 確かめ方

Manual mode に切り替えて、消してよい memory 1 件で `dream forget <name>` を打ってもらう。要素に当たっていれば
権限プロンプトの題名が「Bash command (unsandboxed)」になり、承認すると消えて `MEMORY.md` の索引行も消える。
題名にそれが無ければ sandbox の中で動き、`operation not permitted` で失敗する。
消してよい memory が無ければ、settings の行の有無だけで判定し、その旨を報告に残す。
