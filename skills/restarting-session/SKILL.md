---
name: restarting-session
description: >-
  今のセッションを、同じ会話を持った新しいプロセスとして rex の新しいタブに起こし直し、元のタブを閉じるときに使う。
  plugin を更新した後に新しいバージョンの SessionStart hook と mod を読ませたい、settings や CLAUDE.md を書き換えた、
  コンテキストが逼迫した、「セッションを再起動して」「開き直して」と言われたときに引く。
---

# restarting-session

セッションは、起動したときのバージョンの hook・mod・settings を使い続ける。読み直させるには、新しいプロセスを
起こして続きを渡し、元のプロセスを閉じる。新しいプロセスは `agents-daemon spawn` で同じ rex の session のタブに起こし、
元のタブは新しいセッションが閉じる。

## 前提

`printenv REX_SESSION` が値を出すこと。空なら rex のタブを操作できないので、利用者へ次のコマンドを渡して
元の端末を閉じてもらう (`<id>` は `printenv CLAUDE_CODE_SESSION_ID` の値)。

```sh
claude --resume <id> --fork-session
```

## 形を選ぶ

| 場面 | 形 |
| ---- | ---- |
| 既定 (plugin・settings・CLAUDE.md の更新を読ませる) | fork: spawn の `fork_from` に自分の session ID を書き、会話をそのまま写す |
| コンテキストの逼迫 | 引き継ぎ文: `fork_from` を書かず、続きの要約を `prompt_file` で渡す |

同じ session ID のままの `claude --resume <id>` は使わない。元のプロセスが閉じるまで 2 つのプロセスが同じ transcript に書き、
Remote Control は元に残って新しいプロセスに付かず、元が閉じたときの SessionEnd が作業の記録を終わったことにする。

<!-- cc-premise: 2.1.330 remote-control 動いているセッションを同じ ID で --resume すると、Remote Control は元のプロセスに残る -->

fork は会話を写すが、タスク (`TaskList`) は写さない。新しいセッションは別の session ID になり、タスクは空で始まる。

<!-- cc-premise: 2.1.330 cli --fork-session の先はタスクを持たず、SessionStart の source は fork -->

## 元のセッションの手順

1. 控える。自分の session ID (`printenv CLAUDE_CODE_SESSION_ID`)、rex の session と window (`rex whoami --json` の
   `session_id` と `window_id`)、今のモデル ID (system prompt にある値。`[1m]` のような接尾辞も含めて写す)、
   今の cwd の絶対パス (`pwd`)
2. 引き継ぎ文を `./tmp/restart-brief.md` に Write で書く。形は下の「引き継ぎ文」
3. 起こす指定を `./tmp/restart-spawn.json` に Write で書き、spawn を打つ (欄の意味は agents-daemon:peer-sessions skill の
   `references/rex.md`「peer の Claude セッションを立てる」)

   ```json
   {"repo": "<今の cwd>", "fork_from": "<自分の session ID>", "prompt_file": "./tmp/restart-brief.md", "label": "<今のタブの名前>", "model": "<今のモデル ID>"}
   ```

   ```sh
   ~/.cache/agents-daemon/bin/agents-daemon spawn < ./tmp/restart-spawn.json
   ```

4. 結果の `foreground` が `true` なら、ターンを終えて以後は何もしない。元のタブは新しいセッションが閉じる。
   `false` なら新しいタブを `rex capture` で読み、起動の失敗を利用者へ上げる。元のタブは閉じずに残す

`repo` は今の cwd にする。`--resume` は cwd の project から会話を探すので、worktree の中ならその worktree のパスを書く
(peer-sessions skill の「自分の worktree は渡さない」の例外)。`model` を省くと subagent 用のモデルで起きるので必ず書く。
`fork_from` は spawn の 2026.1121.0 より後のバージョンが受ける。

## 引き継ぎ文

新しいセッションの最初のターンになる。次の部品をこの順で書く。

1. 再起動であることと、元の session ID
2. SessionStart の確かめ。自分の transcript (`~/.claude/projects/<cwd の project>/<新しい session ID>.jsonl`) に、
   hookName が `SessionStart:fork` で command が `shared-rex-session.sh` の記録があること。transcript は出力のある hook しか
   残さず、この hook はタブに起きたセッションで必ず出力する。無ければ発火を確かめられなかったと報告し、元のタブは閉じない
3. worktree の中なら、`EnterWorktree` の `path` に元の cwd を渡して入り直す (sandbox の書き込み先と worktree の lock を取り戻す)
4. 引き継ぐタスク。元の `TaskList` の未完了を subject と状態の行で並べ、`TaskCreate` で作り直すよう書く
5. 元のタブを閉じる。`rex whoami --json` の `window_id` が元の window と違うことを確かめてから
   `rex window close -s <元の rex session> <元の window>` を打つ。同じなら閉じずに止まって利用者へ上げる
6. 続きの作業。再起動の前に途中だったことと、報告の宛先

新しいセッションは別の session ID になる。元の session ID を `sid:` で指す辺があれば、新しいセッションが付け替える
(`agents-daemon:agents-daemon` skill の `references/operations.md`「セッションをまたぐ辺を書く」)。

Remote Control は、新しいセッションに新しい Remote Control の会話として付く。元の会話を開いていた端末は、
元のタブを閉じた時点で切れるので、新しい会話を開き直す。

<!-- cc-premise: 2.1.330 remote-control --fork-session の先は新しい Remote Control の会話を作り、それまでの履歴を上げる -->
