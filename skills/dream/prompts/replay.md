# Replay subagent prompt template — dream Phase 3

dream 7-phase pipeline の Phase 3 Replay で `general-purpose` subagent に投げる prompt template。

旧 SKILL.md と新 SKILL.md (Adopt 前の diff を適用したもの) を fixture として渡し、代表 prompt に対する挙動を観察する。
観察は **副作用ゼロ** で行い、実際の tool は呼ばない。

## 呼び出し側 (dream/SKILL.md の Replay step が組み立てる)

```
prompt = replay.md の本文を read
prompt.replace("<REPRESENTATIVE_PROMPT>", cand.representative_prompt)
prompt.replace("<OLD_SKILL_BODY>", read(cand.old_skill_path))
prompt.replace("<NEW_SKILL_BODY>", read(cand.new_skill_path))

result = Agent(subagent_type="general-purpose", prompt=prompt, ...)
save(result → ${XDG_STATE_HOME:-~/.local/state}/agents-daemon/dream/replays/<candidate-id>.json)
```

## Prompt template (下記を subagent に渡す)

```
あなたは dream 7-phase pipeline の Phase 3 Replay を担当する｡
以下の代表 prompt に対し、2 つの skill 定義 (旧版・新版) それぞれで
「もしこの skill が呼ばれたら Claude Code はどう応答するか」を予測してほしい｡

## 代表 prompt

<REPRESENTATIVE_PROMPT>

## 旧版 SKILL.md (現行)

<OLD_SKILL_BODY>

## 新版 SKILL.md (dream Adopt 提案)

<NEW_SKILL_BODY>

## 要件

- **副作用ゼロ**: 実際に Bash / Edit / Web など tool を呼ばない｡挙動予測だけ｡
- **一貫性**: 旧版と新版で同じ prompt を評価すること (fair な比較)
- **簡潔さ**: 各挙動は 200 文字以内で要点を書く

## 出力形式 (JSON)

{
  "candidate_id": "<CANDIDATE_ID>",
  "representative_prompt": "<REPRESENTATIVE_PROMPT>",
  "old_behavior": {
    "would_trigger": true/false,
    "expected_actions": ["step1", "step2", "..."],
    "output_summary": "..."
  },
  "new_behavior": {
    "would_trigger": true/false,
    "expected_actions": ["..."],
    "output_summary": "..."
  },
  "diff_summary": "旧新の主な違いを 1-2 文で"
}
```

## 呼び出し失敗時の扱い

- rate limit / timeout: candidate を drop し、Stage レポートに「未検証」と明記
- 出力 JSON が parse できない: retry 1 回、再失敗で drop
- 旧新の両方で `would_trigger: false`: candidate を適用しても効果がないため、参考情報として扱う

## 呼び出し例 (Workflow tool から)

```javascript
const replayResult = await agent(
  buildReplayPrompt(candidate, oldBody, newBody),
  { agentType: 'general-purpose', schema: REPLAY_SCHEMA }
)
```
