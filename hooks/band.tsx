import { atom, read, update } from 'claude-code'
import type { EngineInterface, Register, Timer } from 'claude-code'

import type { GraphNode, ParkedMemo } from '../types'
import { loadBoard } from './board-data'
import { daemonBin } from './daemon-bin'
import { COLOR, Graph, ICON, MEMO, isStuck, layers, longest, summary, tags } from './graph'
import { logChange, reason, settle } from './log'
import { KIND_LABEL, parkedMemos } from './parking'

// 依存の DAG (#134) の A: 入力欄の上の band に、依存を earlier → later の向きに並べた低い図と、パーキングのメモの
// 1 行を出す。band は 1 枚で、DAG の図とメモ (#89 の取り出す側) が分け合う。
// - 図: `status --board` の graph。session.start、TaskUpdate、10 秒ごとに読み直す (B の pane を開いている間は B が 3 秒ごと)
// - メモ: このセッションの repo の open のメモ (hooks/park.tsx が読んだ一覧)。TaskUpdate で completed になったら、
//   そのタスクの途中でこのセッションが置いたメモ (`park list --task`) を先頭に寄せる
// 選んだノードとメモ (target) は B の pane と共有する。辺もメモも無ければ出さない。
// cc-premise: 2.1.303 mods ui.render AbovePrompt の props (hasSurvey、bodyColumns) と、TaskUpdate の結果の statusChange で書いた

const board = atom({ plugin: 'agents-daemon', key: 'board' } as const, null)
// parked は hooks/park.tsx が書く。atom は読む module ごとに書く (state の scan が import を辿らない)。
const parked = atom({ plugin: 'agents-daemon', key: 'parked' } as const, [])
const front = atom({ plugin: 'agents-daemon', key: 'front' } as const, [])
const target = atom({ plugin: 'agents-daemon', key: 'target' } as const, '')
const showCompleted = atom({ plugin: 'agents-daemon', key: 'showCompleted' } as const, false)

// メモの行に出すメモの数。残りは数だけ出す。
const SHOWN = 3
// band は全セッションで常に動くので、B の pane (3 秒) より間を空ける。
const POLL_MS = 10000

let timer: Timer | undefined

// startBand は session.start でボードを読み、読み直しの timer を始める。hooks/park.tsx の startPark がメモの一覧を
// 読んだ後に呼ぶ (session.start の hook は hooks/register.ts が持つ)。
export async function startBand($: EngineInterface) {
  await refresh($)
  timer?.cancel()
  timer = $.clock.every(POLL_MS, () => void refresh($))
}

export const registerBand: Register = on => {
  on('tool.call', { tool: 'TaskUpdate' }, async ($, e, next) => {
    const ran = await next(e)
    if (ran.deny === undefined && !ran.isError && ran.result.statusChange?.to === 'completed') {
      await bringForward($, e.taskId)
      await refresh($)
    }

    return ran
  })

  on('ui.render', { component: 'AbovePrompt' }, async ($, e, next) => {
    if (e.props.hasSurvey) return next(e)
    const data = await read($, board)
    const memos = bandMemos(await read($, front), await read($, parked))
    const links = data?.graph.links ?? []
    if (links.length === 0 && memos.length === 0) return next(e)

    const { Box, Button, Text } = $.ui.resolve(e)
    const g = new Graph(data?.graph ?? { nodes: [], links: [], longest_chain: [] })
    const chosen = await read($, target)
    const folded = !(await read($, showCompleted))
    const choose = (key: string) => void update($, target, prev => (prev === key ? '' : key))
    const related = chosen === '' ? undefined : new Set([chosen, ...g.reach(chosen), ...g.upstream(chosen)])
    const columns = layers(g, !folded)
    const width = Math.max(10, Math.floor(e.props.bodyColumns / Math.max(1, columns.length)))
    const rest = memos.length - SHOWN

    const nodeButton = (n: GraphNode) => (
      <Button
        key={`node:${n.key}`}
        variant={n.key === chosen ? 'primary' : 'secondary'}
        dimColor={related !== undefined && !related.has(n.key)}
        onPress={() => choose(n.key)}
      >
        <Text color={COLOR[n.state]}>{ICON[n.state]}</Text>
        {` ${n.title}${tags(n, isStuck(n))}`}
      </Button>
    )

    return (
      <Box flexDirection="column">
        {links.length > 0 && (
          <Box flexDirection="row" gap={2}>
            <Text>{summary(g, memos.length)}</Text>
            <Text color="warning" wrap="truncate-end">
              longest chain: {longest(g, data?.graph.longest_chain ?? [])}
            </Text>
            <Button key="fold" onPress={() => void update($, showCompleted, () => folded)}>
              {folded ? 'Show completed' : 'Hide completed'}
            </Button>
          </Box>
        )}
        {links.length > 0 && <Text dimColor>{`earlier ${'─'.repeat(Math.max(4, e.props.bodyColumns - 16))}► later`}</Text>}
        {links.length > 0 && (
          <Box flexDirection="row">
            {columns.map((column, i) => (
              <Box key={`layer:${i}`} flexDirection="column" width={width} overflow="hidden">
                {column.map(nodeButton)}
              </Box>
            ))}
          </Box>
        )}
        {memos.length > 0 && (
          <Box flexDirection="row" gap={1}>
            <Text color="success">
              {MEMO} {String(memos.length)}
            </Text>
            {memos.slice(0, SHOWN).map(memo => (
              <Button key={`park:${memo.id}`} variant={`park:${memo.id}` === chosen ? 'primary' : 'secondary'} onPress={() => choose(`park:${memo.id}`)}>
                <Text dimColor>[{KIND_LABEL[memo.kind]}]</Text>
                {` ${memo.title}`}
              </Button>
            ))}
            {rest > 0 && <Text dimColor>+{String(rest)}</Text>}
          </Box>
        )}
      </Box>
    )
  })
}

// bandMemos はメモの行の並び: 完了したタスクの途中で置いたメモを先に、残りを新しく置かれた順に。
function bandMemos(first: ParkedMemo[], all: ParkedMemo[]): ParkedMemo[] {
  const ids = new Set(first.map(m => m.id))
  const rest = all.filter(m => !ids.has(m.id)).sort((a, b) => Date.parse(b.at) - Date.parse(a.at))
  return [...first, ...rest]
}

// refresh はボードを読み直す。band は補助なので、失敗は理由をログに残すだけにする。
async function refresh($: EngineInterface) {
  const loaded = await loadBoard($)
  if ('failure' in loaded) {
    await logChange($, 'band-board', 'mod.band.failed', { reason: loaded.failure })
    return
  }
  await settle($, 'band-board')
}

// bringForward は完了したタスクの途中でこのセッションが置いた open のメモを、メモの行の先頭に寄せる。無ければ今の並びを
// 残す。読めなかったときも同じ。band は補助なので、失敗で TaskUpdate の結果を止めない。
async function bringForward($: EngineInterface, taskId: string) {
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
    await update($, front, () => memos)
  } catch (err) {
    // バイナリがまだ build されていない間もここに来る。
    await logChange($, 'band', 'mod.band.failed', { reason: reason(err, 'park list') })
  }
}
