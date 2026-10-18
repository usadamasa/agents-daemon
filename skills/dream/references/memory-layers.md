# 対象の記憶階層

毎セッション自動ロードされる順に並べる。dream skill はこの階層全体を扱う。

| レイヤ | パス | 性質 | dream 対象 |
|-------|------|------|-----------|
| 1. グローバル CLAUDE.md | `~/.claude/CLAUDE.md` | 世界のルール (個人配下の最上位) | ○ |
| 2. プロジェクト CLAUDE.md | `<repo-root>/CLAUDE.md` ・サブディレクトリ CLAUDE.md | プロジェクト・サブツリー固有のルール | ○ |
| 3. auto memory 索引 | `~/.claude/projects/<project-canonical>/memory/MEMORY.md` | 各メモリのフック行を並べた索引 (~200 行上限) | ○ |
| 4. auto memory 本体 | 同ディレクトリ配下の `*.md` | 関連時に想起される個別メモリ | ○ |
| 5. グローバルスキル | `~/.claude/skills/*/SKILL.md` | グローバル配布スキルの本体・description | △ |
| 6. プロジェクトスキル | `<repo>/.claude/skills/*/SKILL.md` | リポジトリ専用スキル | △ |

**△ の意味**: dream はスキル本体の「手順」を書き換えない (skill の改修として別作業で扱う)。
ただし `description` 行の trigger 文言と、SKILL.md と auto memory の間で重複している知見の刈り取りは扱う。

設定 (`~/.claude/settings.json` および `<repo>/.claude/settings.json`)、hook、エージェントは dream の対象外。
これらは別 skill (`claude-config-management`, `permission-optimizer`, 組み込み `/update-config` 等) が担う。

グローバル階層 (1, 3, 4, 5) は、どのリポジトリで作業していても共通の dream 対象になる。
プロジェクト階層 (2, 6) は作業中のリポジトリのものだけが対象。
dream skill はリポジトリ固有のワークフローではなく、
**ユーザー単位の記憶運用** として任意のリポジトリ・任意の cwd から実行できる。
