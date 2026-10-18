import { atom, read, update } from 'claude-code'
import type { EngineInterface, Register } from 'claude-code'

import type { Band, ParkedMemo } from '../types'
import { daemonBin } from './daemon-bin'
import { logChange, reason, settle } from './log'
import { KIND_LABEL, parkedMemos, stamp } from './parking'

// パーキングロット (#89) の取り出す側 (#121 の段 7)。入力欄の上の band にメモを出す。
// - session.start: このセッションの repo の open のメモ (hooks/park.tsx が読んだ一覧)
// - TaskUpdate で completed になったとき: そのタスクの途中でこのセッションが置いたメモ (`park list --task`)
// どちらも `--session` で開いたセッションの domain のメモだけを引く (私用と業務を混ぜない)。
// cc-premise: 2.1.302 mods ui.render AbovePrompt の props (hasSurvey) と、TaskUpdate の結果の statusChange で書いた

const band = atom({ plugin: 'agents-daemon', key: 'band' } as const, null)
// parked は hooks/park.tsx が書く。atom は読む module ごとに書く (state の scan が import を辿らない)。
const parked = atom({ plugin: 'agents-daemon', key: 'parked' } as const, [])

// 主なものとして出すメモの数。残りは数だけ出す。
const SHOWN = 3

// startBand は session.start で、このセッションの repo の open のメモを band に出す。hooks/park.tsx の
// startPark が一覧を読んだ後に呼ぶ (session.start の hook は hooks/register.ts が持つ)。
export async function startBand($: EngineInterface) {
  const memos = await read($, parked)
  if (memos.length === 0) return
  await update($, band, () => ({ heading: `この repo のパーキングのメモ ${memos.length} 件`, memos }))
}

export const registerBand: Register = on => {
  on('tool.call', { tool: 'TaskUpdate' }, async ($, e, next) => {
    const ran = await next(e)
    if (ran.deny === undefined && !ran.isError && ran.result.statusChange?.to === 'completed') {
      await showCompleted($, e.taskId)
    }

    return ran
  })

  on('ui.render', { component: 'AbovePrompt' }, async ($, e, next) => {
    const shown = await read($, band)
    if (e.props.hasSurvey || shown === null) return next(e)

    const { Box, Button, Text } = $.ui.resolve(e)
    // 新しく置かれたものを先に出す。
    const memos = [...shown.memos].sort((a, b) => Date.parse(b.at) - Date.parse(a.at))
    const rest = memos.length - SHOWN

    return (
      <Box flexDirection="column">
        <Box>
          <Text bold>{shown.heading} </Text>
          <Button key="park-band-close" label="閉じる" onPress={() => update($, band, () => null)} />
        </Box>
        {memos.slice(0, SHOWN).map(memo => (
          <Text key={memo.id}>{line(memo)}</Text>
        ))}
        {rest > 0 && <Text dimColor>ほか {rest} 件 (/board で一覧)</Text>}
      </Box>
    )
  })
}

function line(memo: ParkedMemo): string {
  return `・[${KIND_LABEL[memo.kind]}] ${memo.title} ${stamp(memo.at)}`
}

// showCompleted は完了したタスクの途中でこのセッションが置いた open のメモを band に出す。無ければ今の band を残す。
// 読めなかったときも同じ。band は補助なので、失敗で TaskUpdate の結果を止めない。
async function showCompleted($: EngineInterface, taskId: string) {
  try {
    const argv = [
      await daemonBin($),
      'park',
      'list',
      '--status',
      'open',
      '--task',
      taskId,
      '--session',
      await $.session.id(),
      '--cwd',
      await $.session.cwd(),
    ]
    const { exitCode, stdout, stderr } = await $.process.run(argv, { timeoutMs: 10000 })
    if (exitCode !== 0) {
      await logChange($, 'band', 'mod.band.failed', { reason: stderr.trim() || `exit ${exitCode}` })
      return
    }
    const memos = parkedMemos(stdout)
    await settle($, 'band')
    if (memos.length === 0) return
    const next: Band = { heading: `タスク #${taskId} の途中で置いたメモ ${memos.length} 件`, memos }
    await update($, band, () => next)
  } catch (err) {
    // バイナリがまだ build されていない間もここに来る。
    await logChange($, 'band', 'mod.band.failed', { reason: reason(err, 'park list') })
  }
}
