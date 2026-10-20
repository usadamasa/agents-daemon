import { atom, read, update } from 'claude-code'
import type { EngineInterface, Register, Timer } from 'claude-code'

import type { GraphNode, ParkedMemo } from '../types'
import { reloadForBand } from './board-data'
import { daemonBin } from './daemon-bin'
import { COLOR, Graph, ICON, MEMO, cells, fit, isStuck, layers, longest, summary, tags } from './graph'
import { logChange, reason, settle } from './log'
import { parkedMemos } from './parking'

// 依存の DAG (#134) の A: 入力欄の上の band に、依存を earlier → later の向きに並べた低い図と、パーキングのメモの
// 1 行を出す。band は 1 枚で、DAG の図とメモ (#89 の取り出す側) が分け合う。
// - 図: `status --board` の graph。session.start、TaskUpdate、10 秒ごとに読み直す (B の pane を開いている間は B が 3 秒ごと)
// - メモ: このセッションの repo の open のメモ (同じボードの here の行、hooks/board-data.ts の parked)。TaskUpdate で
//   completed になったら、そのタスクの途中でこのセッションが置いたメモ (`park list --task`) を先頭に寄せる。
//   寄せるのは並びだけで、open でなくなったメモは次の読み直しで消える
// 選んだノードとメモ (target) は B の pane と共有する。辺もメモも無ければ出さない。
// cc-premise: 2.1.303 mods ui.render AbovePrompt の props (hasSurvey、bodyColumns) と、TaskUpdate の結果の statusChange で書いた
// cc-premise: 2.1.303 mods Button の plain は端末で [ ] を描かず、variant より優先される。ノードとメモは plain、操作は枠付き

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
// 行頭の印。選んだものだけに › を出し、ほかは同じ幅の空白で揃える。
const GUTTER = 2

let timer: Timer | undefined

// startBand は session.start でボードを読み、読み直しの timer を始める (session.start の hook は hooks/register.ts が持つ)。
// 読んだボードの here の行のメモが hint 行 (hooks/park.tsx) の数にもなる。
export async function startBand($: EngineInterface) {
  await reloadForBand($)
  timer?.cancel()
  timer = $.clock.every(POLL_MS, () => void reloadForBand($))
}

export const registerBand: Register = on => {
  on('tool.call', { tool: 'TaskUpdate' }, async ($, e, next) => {
    const ran = await next(e)
    if (ran.deny === undefined && !ran.isError && ran.result.statusChange?.to === 'completed') {
      await bringForward($, e.taskId)
      await reloadForBand($)
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
    // 列の間に 1 桁空ける。ノードは 1 行に切り詰め、band が伸びて最後の行のメモが見えなくならないようにする。
    const width = Math.max(10, Math.floor((e.props.bodyColumns - (columns.length - 1)) / Math.max(1, columns.length)))
    // 1 行目と軸、メモの行を除いた高さに列のノードを収める。収まらない分は +n にまとめる。
    const perColumn = Math.max(1, e.props.maxRows - 2 - (memos.length > 0 ? 1 : 0))
    const rest = memos.length - SHOWN
    // メモは 1 行に SHOWN 件並べる。先頭の「◇ n」と、1 件ごとの行頭の印と種類の分を除いた幅に title を収める。
    const memoRoom = Math.max(6, Math.floor((e.props.bodyColumns - 8) / SHOWN) - GUTTER - 7)

    // ノードとメモは枠なし (plain) の Button にして、枠付きの Button は操作 (fold) だけに使う。plain は variant より
    // 優先されるので、選んだものは行頭の › と太字で示す。
    const nodeButton = (n: GraphNode) => {
      const tag = tags(n, isStuck(n))
      const isSel = n.key === chosen
      return (
        <Box key={`box:node:${n.key}`} flexDirection="row">
          <Text>{isSel ? '› ' : ' '.repeat(GUTTER)}</Text>
          <Button key={`node:${n.key}`} plain dimColor={related !== undefined && !related.has(n.key)} onPress={() => choose(n.key)}>
            <Text color={COLOR[n.state]}>{ICON[n.state]}</Text>
            {/* 行頭の印と、記号と空白の 2 桁を除いた幅に title を収める。 */}
            <Text bold={isSel ? true : undefined}>{` ${fit(n.title, Math.max(2, width - GUTTER - 2 - cells(tag)))}${tag}`}</Text>
          </Button>
        </Box>
      )
    }
    const column = (nodes: GraphNode[], i: number) => {
      const shown = nodes.length > perColumn ? nodes.slice(0, perColumn - 1) : nodes
      return (
        <Box key={`layer:${i}`} flexDirection="column" width={width} overflow="hidden">
          {shown.map(nodeButton)}
          {shown.length < nodes.length && <Text dimColor>+{String(nodes.length - shown.length)}</Text>}
        </Box>
      )
    }

    return (
      <Box flexDirection="column">
        {links.length > 0 && (
          <Box flexDirection="row" gap={2}>
            <Box flexShrink={0}>
              <Text>{summary(g, memos.length)}</Text>
            </Box>
            <Box flexGrow={1} flexShrink={1} overflow="hidden">
              <Text color="warning" wrap="truncate-end">
                longest chain: {longest(g, data?.graph.longest_chain ?? [])}
              </Text>
            </Box>
            <Box flexShrink={0}>
              <Button key="fold" onPress={() => void update($, showCompleted, () => folded)}>
                {folded ? 'Show completed' : 'Hide completed'}
              </Button>
            </Box>
          </Box>
        )}
        {links.length > 0 && <Text dimColor>{`earlier ${'─'.repeat(Math.max(4, e.props.bodyColumns - 16))}► later`}</Text>}
        {links.length > 0 && (
          <Box flexDirection="row" gap={1}>
            {columns.map(column)}
          </Box>
        )}
        {memos.length > 0 && (
          <Box flexDirection="row" gap={1}>
            <Text color="success">
              {MEMO} {String(memos.length)}
            </Text>
            {memos.slice(0, SHOWN).map(memo => {
              const isSel = `park:${memo.id}` === chosen
              return (
                <Box key={`box:park:${memo.id}`} flexDirection="row">
                  <Text>{isSel ? '› ' : ' '.repeat(GUTTER)}</Text>
                  <Button key={`park:${memo.id}`} plain onPress={() => choose(`park:${memo.id}`)}>
                    <Text dimColor>[{memo.kind}]</Text>
                    <Text bold={isSel ? true : undefined}>{` ${fit(memo.title, memoRoom)}`}</Text>
                  </Button>
                </Box>
              )
            })}
            {rest > 0 && <Text dimColor>+{String(rest)}</Text>}
          </Box>
        )}
      </Box>
    )
  })
}

// bandMemos はメモの行の並び: 完了したタスクの途中で置いたメモ (first、id の順) のうち open のものを先に、残りを
// 新しく置かれた順に。出すのは open (open の一覧) にあるものだけで、first に残った id でも open でなければ出さない。
function bandMemos(first: string[], open: ParkedMemo[]): ParkedMemo[] {
  const byId = new Map(open.map(m => [m.id, m]))
  const front = first.flatMap(id => byId.get(id) ?? [])
  const ids = new Set(first)
  const rest = open.filter(m => !ids.has(m.id)).sort((a, b) => Date.parse(b.at) - Date.parse(a.at))
  return [...front, ...rest]
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
    await update($, front, () => memos.map(m => m.id))
  } catch (err) {
    // バイナリがまだ build されていない間もここに来る。
    await logChange($, 'band', 'mod.band.failed', { reason: reason(err, 'park list') })
  }
}
