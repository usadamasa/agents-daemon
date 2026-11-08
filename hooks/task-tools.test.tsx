import type { ToolDescribeInput } from 'claude-code'
import { expect, test } from 'claude-code/testing'

// engine が ToolSearch の裏に置いた built-in の tool の入力。
function deferred(tool: string): ToolDescribeInput {
  return { tool, description: `${tool} の説明`, isDeferred: true, provider: { plugin: 'engine', tier: 'core' } }
}

for (const tool of ['TaskCreate', 'TaskUpdate']) {
  test(`${tool} は説明文を変えずに prompt の tool 一覧へ置く`, async ($, on) => {
    on('tool.describe', async (_$, e) => ({ description: e.description, isDeferred: e.isDeferred }))

    const res = await $.tool.describe(deferred(tool))

    expect(res.isDeferred).toBe(false)
    expect(res.description).toBe(`${tool} の説明`)
  })
}

for (const tool of ['TaskList', 'TaskGet']) {
  test(`${tool} は ToolSearch の裏に残す`, async ($, on) => {
    on('tool.describe', async (_$, e) => ({ description: e.description, isDeferred: e.isDeferred }))

    const res = await $.tool.describe(deferred(tool))

    expect(res.isDeferred).toBe(true)
  })
}
