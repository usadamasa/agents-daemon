# Gate rubric — dream Phase 5 の判定基準

dream 7-phase pipeline の Phase 5 Gate で使う LLM judge 用 rubric。4 軸で採点し、compound score と
compliance 加点で最終 verdict を決める。3 judge の majority vote で pipeline 全体の verdict を確定する。

**用途**: dream の Adopt 対象を承認するかどうかを judge subagent が採点する。
本 rubric の本文が judge の prompt に埋め込まれる (`prompts/judge.md` の `<RUBRIC_BODY>` に substitution される)。

## Rubric 全体構造

各 candidate に対して、旧 SKILL.md と新 SKILL.md (Adopt 前 diff 適用) の挙動を 4 軸で比較し、次の形式で出力する。

```jsonc
{
  "candidate_id": "...",
  "trigger_score": 0-5,      // trigger 精度 (FN 重視、weight ×3)
  "output_score": 0-5,       // 出力適合 (weight ×1.5)
  "token_delta": -1.0..+1.0, // token 効率 (相対増減率、+20% までは neutral)
  "compliance_score": 0-5,   // compliance/safety 加点枠 (compliance_score >= 4 で自動 accept 昇格)
  "compound_score": <計算値>,
  "verdict": "accept" | "reject" | "revise",
  "reason": "..."
}
```

## 軸 1: Trigger 精度 (0-5 点、weight ×3)

想定 prompt に対して skill が正しく triggering されるか。
**triggering 漏れ (FN) を過剰 triggering (FP) より 3 倍重く見る**。skill が想起されなければ effect zero で、
後段の output_score も compliance_score も無意味になる。
一方で過剰 triggering は context を汚す (token cost) だけで挙動そのものは壊さない、という非対称性を反映する。

| スコア | 意味 |
|-------|------|
| 5 | 想定 prompt 全てで triggering、無関係 prompt では発火しない |
| 4 | 想定 prompt で発火するが、まれに過剰 triggering あり (FP 少数、FN ゼロ) |
| 3 | 想定 prompt の 80%+ で発火、または過剰 triggering が顕著 (FN 少数 or FP 多数) |
| 2 | 想定 prompt の 60% 未満で発火 (triggering 漏れ多い、致命的) |
| 1 | 想定 prompt で発火せず (skill が事実上死んでいる) |
| 0 | 判定不能 (fixture 不足) |

**採点指針**: FN と FP のどちらもある場合、FN を優先して減点する。
例: FN 1 件 + FP 1 件は「4」ではなく「3」にする。FN ゼロ + FP 3 件は「4」を維持してよい。

## 軸 2: 出力適合 (0-5 点、weight ×1.5)

skill が返した成果が期待通りか。意味的に一致していれば表現差は許容する。

| スコア | 意味 |
|-------|------|
| 5 | 期待出力と厳密一致 (or 意味的に完全一致) |
| 4 | 意図一致 + 補足情報がある (over-deliver、有益) |
| 3 | 意図一致だが表現・順序・詳細度が異なる (essential な要素は揃う) |
| 2 | 部分的に意図と合致するが essential な要素が欠落 |
| 1 | 出力が期待から大きく乖離 (別のことをしている) |
| 0 | 出力なし or エラー |

**判定基準**: essential keyword は、rubric の適用時に candidate の diagnosis 節から特定する。
期待出力の essential keyword のうち 70% 以上が新出力に含まれれば 4 点以上、
50-70% で 3 点、それ未満は 2 点以下とする。

## 軸 3: Token 効率 (−1.0〜+1.0)

同じ結果を出すのに token 消費が減ったか。SKILL.md が常時ロードされる分の cost に効くので無視できないが、
compliance/safety 由来の inline を潰さない程度に寛容 (+20% までは neutral) とする。

- **`token_delta = (新 - 旧) / 旧`** を計算 (小数、文字数比で近似可)
- `token_delta <= -0.1`: 明確な効率化 (bonus 材料)
- `-0.1 < token_delta <= +0.2`: neutral (許容範囲、penalty 無し)
- `+0.2 < token_delta <= +0.4`: 微増 (改善内容が正当化できるか要判断、compound で penalty)
- `token_delta > +0.4`: 顕著な増加 (原則 reject 候補、ただし compliance 加点で救済可)

## 軸 4: Compliance / Safety 加点枠 (0-5 点、weight ×1.5、加えて自動昇格枠)

再発シグナル (harvest の `feedback_signals` や `failed_invocations` に該当 skill 由来がある) がゼロの候補でも、
次のような予防的ガードレールは通す評価軸。

**対象**:

- 外部個人 PII 保護 (面接候補者名・顧客名・候補者 ID 等)
- コンプライアンス要件由来のガードレール (法務・情報セキュリティ・監査要件)
- 事故時のコストが取り返しつかないもの (git force push、DB 破壊、公開誤送信、認証情報流出 等)

| スコア | 意味 |
|-------|------|
| 5 | 事故コスト極大 (法的責任・信用毀損・情報漏洩) の予防 |
| 4 | 事故コスト大 (作業やり直し不可・external stakeholder への影響) の予防 |
| 3 | 事故コスト中 (再作業で回復可能・internal のみ) の予防 |
| 0 | compliance/safety マター ではない (通常の改善候補) |

**自動昇格ルール**: `compliance_score >= 4` の場合、compound_score が accept 閾値 (21) 未満でも `accept` に昇格させる。
ただし `token_delta > +0.5` (skill 肥大化 50% 超) の場合のみ `revise` に降格 (safety でも歯止め)。

## 最終判定 (compound score と verdict)

各 judge が 4 軸で採点し、次の式で compound score を計算する。

```
compound = trigger_score * 3.0                       # FN 3 倍重視
         + output_score * 1.5                        # 出力品質重視
         + max(0, -token_delta * 10)                 # token 減 bonus (最大 +10)
         - max(0, (token_delta - 0.2) * 10)          # +20% 超過分に penalty (0 if <= +20%)
         + compliance_score * 1.5                    # compliance 加点
```

judge ごとの verdict は次のとおり。

- `compound_score >= 21`: `accept`
- `compound_score <= 12`: `reject`
- それ以外 (13-20): `revise`
- ただし `compliance_score >= 4` かつ `token_delta <= +0.5` なら `accept` に昇格

3 judge の majority (2/3 以上) で最終 verdict を決定。
split (accept 1 : reject 1 : revise 1 等 majority 無し) は `revise`。
