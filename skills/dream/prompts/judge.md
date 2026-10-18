# Judge subagent prompt template — dream Phase 5

dream の 7-phase pipeline で、Phase 5 Gate が 3 judge を並列に走らせるときの prompt template。

**役目**: Replay の出力 (旧版 vs 新版の挙動比較) を rubric に基づいて採点する。
3 judge の majority vote で最終 verdict を決める。

## 呼び出し側 (dream/SKILL.md の Gate step が組み立てる)

```
rubric = references/gate-rubric.md の本文を read
replay_result = ${XDG_STATE_HOME:-~/.local/state}/agents-daemon/dream/replays/<cand-id>.json を read

# 3 judge を並列に走らせる
verdicts = parallel([
  agent(build_judge_prompt(rubric, replay_result, judge=1), agentType='general-purpose', schema=VERDICT),
  agent(build_judge_prompt(rubric, replay_result, judge=2), agentType='general-purpose', schema=VERDICT),
  agent(build_judge_prompt(rubric, replay_result, judge=3), agentType='general-purpose', schema=VERDICT),
])

# majority vote
final = majority_verdict(verdicts)  # accept 2+ = accept, reject 2+ = reject, split = revise
save(final → ${XDG_STATE_HOME:-~/.local/state}/agents-daemon/dream/verdicts/<cand-id>.json)
```

## Prompt template (下記を subagent に渡す、judge_num は 1/2/3)

```
あなたは dream 7-phase pipeline の Phase 5 Gate の Judge #<JUDGE_NUM> です｡
以下の Replay 結果と rubric に基づき、この skill 変更提案を採点してください｡

## Rubric

<RUBRIC_BODY>

## Replay 結果 (candidate <CANDIDATE_ID>)

<REPLAY_RESULT_JSON>

## 出力形式 (JSON)

{
  "candidate_id": "<CANDIDATE_ID>",
  "judge_num": <JUDGE_NUM>,
  "trigger_score": 0-5,
  "output_score": 0-5,
  "token_delta": -1.0..+1.0,
  "compliance_score": 0-5,
  "compound_score": <計算値>,
  "verdict": "accept" | "reject" | "revise",
  "reason": "採点根拠 (100-200 文字)"
}

## 注意

- 他の judge の結果は見えない (独立採点)
- rubric の compound score 式に従うこと (自分で式を発明しない)
- 迷ったら "revise" を出す (accept でも reject でもない中間)
- Replay 結果が `would_trigger: false` (旧新両方) なら output_score は 0 (skill 効果ゼロ)
```

## 呼び出し失敗時の扱い

- 3 judge のうち 2 以上が失敗: candidate を drop
- 1 judge のみ失敗: 残 2 judge の合議 (unanimous ならその verdict、split なら "revise")
- rate limit: 30 秒待って retry 1 回

## Workflow tool 呼び出し例

```javascript
const verdicts = (await parallel(
  [1, 2, 3].map(n => () =>
    agent(buildJudgePrompt(rubric, replay, n), { agentType: 'general-purpose', schema: VERDICT_SCHEMA })
  )
)).filter(Boolean)

const final = majorityVerdict(verdicts)
```
