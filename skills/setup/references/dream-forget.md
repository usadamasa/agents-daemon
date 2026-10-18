# dream forget を sandbox の外で動かす

`agents-daemon dream forget` は auto memory (`~/.claude/projects/<project>/memory/*.md`) を消す。
このディレクトリは Claude Code 組み込みの sandbox 保護対象で、`allowWrite` で再許可する表現が無いため、
sandbox の中では `operation not permitted` になる。`dream forget` だけを `sandbox.excludedCommands` で外に出す。
`rm` を出さない理由と削除範囲の制約は `agents-daemon:dream` skill の `references/layer-auto-memory.md`。

<!-- cc-premise: 2.1.302 sandbox excludedCommands は Bash 許可ルールと同じ構文で呼び出しの文字列に当たる。変数から来る command name は sandbox に残る -->

## 足す行

利用者の `~/.claude/settings.json`:

```json
{
  "sandbox": {
    "excludedCommands": ["*/agents-daemon/bin/agents-daemon dream forget *"]
  }
}
```

## 照合の規則

- 要素は `Bash(...)` の許可ルールと同じ構文で、`*` は任意の文字列に当たる。呼び出しの文字列そのものと照合するので、
  basename だけの `agents-daemon dream forget *` は絶対パスで呼んだコマンドに当たらない。先頭の `*/` が
  `~/.cache` と `/Users/x/.cache` と `XDG_CACHE_HOME` の実パスを吸収する
- 呼ぶ側は `~/.cache/agents-daemon/bin/agents-daemon dream forget <name>` と literal のパスで打つ。
  `"${XDG_CACHE_HOME:-$HOME/.cache}/..."` のように command name が変数から来る呼び出しは、要素に当たっても
  sandbox に残る。`cd` や `$(...)` や `>` を同じ呼び出しに混ぜても同じ
- 除外しても permission flow は通る (auto mode なら classifier、Manual なら prompt)

## 確かめ方

Manual mode に切り替えて、消してよい memory 1 件で `dream forget <name>` を打ってもらう。要素に当たっていれば
権限プロンプトの題名が「Bash command (unsandboxed)」になり、承認すると消えて `MEMORY.md` の索引行も消える。
題名にそれが無ければ sandbox の中で動き、`operation not permitted` で失敗する。
消してよい memory が無ければ、settings の行の有無だけで判定し、その旨を報告に残す。
