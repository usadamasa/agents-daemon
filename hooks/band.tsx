import { atom, read, update } from 'claude-code'
import type { EngineInterface, Register, Timer } from 'claude-code'

import type { ParkedMemo } from '../types'
import { reloadForBand } from './board-data'
import { daemonBin } from './daemon-bin'
import { Graph, MEMO, fit, longest, summary, unlinked } from './graph'
import { logChange, reason, settle } from './log'
import { parkedMemos } from './parking'

// 依存の DAG (#134) の A: 入力欄の上の band に、依存の数と longest chain の 1 行 (B の pane を開く合図) と、
// 辺の書き忘れを疑う渡し先の 1 行 (#172)、パーキングのメモの 1 行を出す。図は描かない (辺を読んで操作する面は
// B の pane に 1 つ)。
// - 数: `status --board` の graph。session.start、TaskUpdate、10 秒ごとに読み直す (B の pane を開いている間は B が 3 秒ごと)
// - no edge: model が SendMessage で送った宛先 (session.send の origin model、届いたもの) のうち、ボードの生きている
//   セッションで `edge add` の辺が無いもの (hooks/graph.ts の unlinked)。渡す側が辺を書くのは peer-sessions skill の
//   手順で、書かれるかそのセッションが済むと消える。辺の要らない送信 (返事、報告) だったら名前を押して外す
// - メモ: このセッションの repo の open のメモ (同じボードの here の行、hooks/board-data.ts の parked)。TaskUpdate で
//   completed になったら、そのタスクの途中でこのセッションが置いたメモ (`park list --task`) を先頭に寄せる。
//   寄せるのは並びだけで、open でなくなったメモは次の読み直しで消える
// 選んだメモ (target) は B の pane と共有する。辺も渡し先もメモも無ければ出さない。
// cc-premise: 2.1.303 mods ui.render AbovePrompt の props (hasSurvey、bodyColumns) と、TaskUpdate の結果の statusChange で書いた
// cc-premise: 2.1.303 mods Button の plain は端末で [ ] を描かず、variant より優先される。メモは plain
// cc-premise: 2.1.306 mods session.send は SendMessage tool と $.session.send の両方で起き、e.origin.kind (model / plugin) で見分ける。
//   e.to は tool に書かれた宛先そのまま (名前、末尾に ` [ref]` が付くことがある)

const board = atom({ plugin: 'agents-daemon', key: 'board' } as const, null)
// parked は hooks/board-data.ts が書く。atom は読む module ごとに書く (state の scan が import を辿らない)。
const parked = atom({ plugin: 'agents-daemon', key: 'parked' } as const, [])
const front = atom({ plugin: 'agents-daemon', key: 'front' } as const, [])
const target = atom({ plugin: 'agents-daemon', key: 'target' } as const, '')
const handed = atom({ plugin: 'agents-daemon', key: 'handed' } as const, [])

// メモの行に出すメモの数。残りは数だけ出す。
const SHOWN = 3
// band は全セッションで常に動くので、B の pane (3 秒) より間を空ける。
const POLL_MS = 10000
// 行頭の印。選んだものだけに › を出し、ほかは同じ幅の空白で揃える。
const GUTTER = 2

let timer: Timer | undefined

// startBand は session.start でボードを読み、読み直しの timer を始める (session.start の hook は hooks/register.ts が持つ)。
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

  // model が SendMessage を送った宛先を覚える。mod 自身の送信 (origin plugin、メモの手渡し) は依存ではないので覚えない。
  // band は補助なので、覚えられなくても送信の結果は変えない。
  on('session.send', async ($, e, next) => {
    const sent = await next(e)
    if (e.origin.kind === 'model' && sent.isDelivered) {
      const name = recipient(e.to)
      try {
        await update($, handed, prev => (prev.includes(name) ? prev : [...prev, name]))
      } catch (err) {
        await logChange($, 'band', 'mod.band.failed', { reason: reason(err, 'session.send') })
      }
    }

    return sent
  })

  on('ui.render', { component: 'AbovePrompt' }, async ($, e, next) => {
    if (e.props.hasSurvey) return next(e)
    const data = await read($, board)
    const memos = bandMemos(await read($, front), await read($, parked))
    const links = data?.graph.links ?? []
    const g = new Graph(data?.graph ?? { nodes: [], links: [], longest_chain: [], rows: [], rows_with_completed: [] })
    const missing = unlinked(g, await read($, handed))
    if (links.length === 0 && missing.length === 0 && memos.length === 0) return next(e)

    const { Box, Button, Text } = $.ui.resolve(e)
    const chosen = await read($, target)
    const choose = (key: string) => void update($, target, prev => (prev === key ? '' : key))
    const forget = (name: string) => void update($, handed, prev => prev.filter(n => n !== name))
    const rest = memos.length - SHOWN
    // メモは 1 行に SHOWN 件並べる。先頭の「◇ n」と、1 件ごとの行頭の印と種類の分を除いた幅に title を収める。
    const memoRoom = Math.max(6, Math.floor((e.props.bodyColumns - 8) / SHOWN) - GUTTER - 7)

    // メモは枠なし (plain) の Button。plain は variant より優先されるので、選んだものは行頭の › と太字で示す。
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
          </Box>
        )}
        {missing.length > 0 && (
          <Box flexDirection="row" gap={1}>
            <Text color="warning">no edge:</Text>
            {missing.map(name => (
              <Button key={`handed:${name}`} plain onPress={() => forget(name)}>
                {name}
              </Button>
            ))}
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

// recipient は SendMessage の宛先からセッション名を取る。同名が複数あるときに付く末尾の ` [ref]` を落とす。
export function recipient(to: string): string {
  return to.replace(/\s+\[[^\]]*\]$/, '').trim()
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
