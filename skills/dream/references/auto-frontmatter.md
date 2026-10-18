# Claude Code が自動管理する frontmatter (dream 対象外)

auto memory の各メモリファイルには、Claude Code 本体が保存時に以下の metadata を自動付与する。
これらは本体が管理する内部状態であり、dream で削除しても次回保存で再付与されるため、**手動編集の対象外** とする。

- `metadata.originSessionId`: メモリを生成・更新したセッションの UUID
- `metadata.node_type: memory`: ノード種別の判別フラグ

dream で整える対象は `name` / `description` / `metadata.type` (`user`/`feedback`/`project`/`reference`) のみ。
本体が付与する原始メタデータには触れない (Edit しても再付与で無駄な diff になり、
原因不明の差分として後続作業者を混乱させる)。

`type` フィールドの書き方には、直書き (`type: feedback`) とネスト (`metadata.type: feedback`) がある。
どちらで出力されるかは本体の挙動次第でケースごとに変わる可能性があるため、
dream では形式の統一を強制せず、本体の出力した形を尊重する。
