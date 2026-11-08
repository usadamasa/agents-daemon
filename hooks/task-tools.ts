import type { Register } from 'claude-code'

// 依存図のタスクと辺の after は model の TaskCreate から始まるが、ToolSearch の裏にあると model は呼ばずに進める (#499)。
// 呼ぶ頻度の高い 2 つだけを prompt の一覧へ置く。説明文は engine のまま。
// cc-premise: 2.1.319 mods tool.describe の isDeferred: false で built-in の tool を prompt の一覧へ置く

export const registerTaskTools: Register = on => {
  on('tool.describe', { tool: 'TaskCreate' }, async (_$, e, next) => ({ ...(await next(e)), isDeferred: false }))
  on('tool.describe', { tool: 'TaskUpdate' }, async (_$, e, next) => ({ ...(await next(e)), isDeferred: false }))
}
