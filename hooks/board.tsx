import { atom, read, update } from 'claude-code'
import type { EngineInterface, Register, Timer } from 'claude-code'

import type { BoardData, BoardRow, GraphLink, GraphNode, GraphRow, ParkingCard } from '../types'
import { loadBoard } from './board-data'
import { daemonBin } from './daemon-bin'
import { COLOR, Graph, ICON, MEMO, candidates, cells, chain, fit, isBlocked, splitLanes, summary, tags } from './graph'
import { logChange, logEvent, settle } from './log'
import type { Fields } from './log'
import {
  adopt,
  canAdopt,
  discard,
  discardStale,
  handOff,
  isStale,
  issueDestinations,
  issueText,
  lastPlacedAt,
  memoLabel,
  memoLines,
  stamp,
  taskDestinations,
  taskText,
} from './parking'

// 依存の DAG (#134) の B: 辺をレーンで描いた行 (graph.rows、上流が上) を dock の pane に描き、選んだノードの詳細と
// 操作を出す mod。中身は `agents-daemon status --board` (internal/board) の graph と rows (メモの送り先)。ここは描くことと
// 送ることだけを持つ。
// pane は会話の横 (dock) に置かれるときだけ出す。入力欄の上 (inline) は A の band が受け持つ。
// cc-premise: 2.1.303 mods pane の placement (dock / inline)、command.run の presentation.isFullscreen、ui.close
// cc-premise: 2.1.302 mods pane / clock.every / process.run / session.send / session.cwd の API で書いた
// cc-premise: 2.1.303 mods Button の plain は端末で [ ] を描かず、variant より優先される。行は plain、操作は枠付き

const PANE = 'agents-daemon-board'
const TITLE = 'Tasks'
// daemon は tick ごとに status.json を書き直し、タスクの記録は Task 系ツールのたびに変わる。
// それより細かく読んでも変わらないので数秒おきにする。
const POLL_MS = 3000
// 行頭の印。選んだ行だけに › を出し、ほかの行は同じ幅の空白で揃える。
const GUTTER = 2
// STALE_DAYS は「古い」メモの日数。週次の棚卸しを 4 回越したメモは文脈が薄れている。daemon の housekeeping は open を
// 90 日で黙って消す (internal/parking の OpenRetention) が、その前に人が見て捨てる区切りをここに置く。
const STALE_DAYS = 30

const board = atom({ plugin: 'agents-daemon', key: 'board' } as const, null)
const error = atom({ plugin: 'agents-daemon', key: 'error' } as const, '')
// target は選んだノードの Key かメモの Key (park:<id>)。A の band と共有する。
const target = atom({ plugin: 'agents-daemon', key: 'target' } as const, '')
const notice = atom({ plugin: 'agents-daemon', key: 'notice' } as const, '')
const showCompleted = atom({ plugin: 'agents-daemon', key: 'showCompleted' } as const, false)
const picking = atom({ plugin: 'agents-daemon', key: 'picking' } as const, false)

// timer はボードの読み直し。pane を開いている間だけ動かす。
let timer: Timer | undefined
// closing は inline に描かれて閉じにかかったか。閉じるまでに描画がもう一度走っても、知らせを重ねない。
let closing = false

// startBoard は session.start で /board を登録する (session.start の hook は hooks/register.ts が持つ)。
export async function startBoard($: EngineInterface) {
  await $.command.register({
    name: 'board',
    description: 'agents-daemon の依存のボードを会話の横に開き、選んだタスクのセッションへ指示を送る',
  })
}

export const registerBoard: Register = on => {
  on('command.run', { command: 'board' }, async ($, e) => {
    if (!e.presentation.isFullscreen) {
      return { text: '依存のボードは fullscreen のレイアウトで、会話の横 (dock) にだけ開きます。この画面では開きません。' }
    }
    closing = false
    await refresh($, true)
    timer?.cancel()
    timer = $.clock.every(POLL_MS, () => void refresh($))
    await $.ui.open({ id: PANE, title: TITLE })

    return { text: 'agents-daemon のボードを開きました。' }
  })

  on('ui.close', async ($, e, next) => {
    if (e.id === PANE) {
      timer?.cancel()
      timer = undefined
    }

    return next(e)
  })

  on('ui.render', { component: 'Pane', requestId: PANE }, async ($, e) => {
    const elements = $.ui.resolve(e)
    const { Box, Text, Button } = elements
    // 端末を狭めて入力欄の上に置かれたら閉じる。入力欄の上は A の band が受け持つ。
    if (e.props.placement === 'inline') {
      if (!closing) {
        closing = true
        await $.ui.close({ id: PANE })
        // 閉じた pane の中の文面は誰にも見えないので、toast で知らせる。
        await $.ui.toast('会話の横に置けないため、依存のボードを閉じました (端末を 110 桁以上にすると開けます)')
      }
      return <Text dimColor>会話の横に置けないため、ボードを閉じました。</Text>
    }
    // mobile には入力欄が無い。ボードは見せて、送信だけ出さない。
    const Input = 'Input' in elements ? elements.Input : undefined
    const data = await read($, board)
    const failure = await read($, error)
    const chosen = await read($, target)
    const sent = await read($, notice)
    const withCompleted = await read($, showCompleted)
    const isPicking = await read($, picking)
    const self = await $.session.id()

    if (data === null) {
      return (
        <Box flexDirection="column">
          {failure !== '' && <Text color="red">ボードを読めません: {failure}</Text>}
          {failure === '' && <Text dimColor>読み込み中…</Text>}
        </Box>
      )
    }

    const g = new Graph(data.graph)
    const memos = data.rows.flatMap(row => row.parking)
    const now = await $.clock.now()
    const stale = memos.filter(memo => isStale(memo, now, STALE_DAYS)).length
    const selected = g.node(chosen)
    const chosenMemo = findMemo(data.rows, chosen)
    const rows = withCompleted ? data.graph.rows_with_completed : data.graph.rows

    const choose = (key: string) => {
      void update($, target, () => key)
      void update($, notice, () => '')
      void update($, picking, () => false)
    }
    // act は操作を走らせ、結果を notice に出してボードを読み直す。
    const act = async (run: () => Promise<string>) => {
      const message = await run()
      await update($, notice, () => message)
      await refresh($)
    }
    const send = async (node: GraphNode, text: string) => {
      if (!canSend(node, self) || text.trim() === '') return
      await update($, notice, () => '')
      await act(() => deliver($, node, instruction(node, text), 'instruction'))
    }

    // 行は枠なし (plain) の Button にして、枠付きの Button は操作だけに使う。レーンの罫線は薄く描き、ノードの記号の
    // 左右に分けて置く。辺をつなぐだけの行は罫線だけ。
    const row = (r: GraphRow, i: number) => {
      const { left, right } = splitLanes(r.lanes)
      const n = r.node === undefined ? undefined : g.node(r.node)
      if (n === undefined) {
        return (
          <Box key={`box:lane:${i}`} flexDirection="row">
            <Text>{' '.repeat(GUTTER)}</Text>
            <Text dimColor>{left}</Text>
          </Box>
        )
      }
      const isSel = n.key === chosen
      const tag = tags(n, isBlocked(n))
      // 行頭の印、レーン、記号と空白を除いた幅に title を収める。
      const room = Math.max(4, e.props.bodyColumns - GUTTER - cells(left) - 1 - cells(right) - 1 - cells(tag))
      return (
        <Box key={`box:row:${n.key}`} flexDirection="row">
          <Text>{isSel ? '› ' : ' '.repeat(GUTTER)}</Text>
          <Text dimColor>{left}</Text>
          <Button key={`row:${n.key}`} plain onPress={() => choose(isSel ? '' : n.key)}>
            <Text color={COLOR[n.state]}>{ICON[n.state]}</Text>
            <Text dimColor>{right}</Text>
            <Text bold={isSel ? true : undefined}>{` ${fit(n.title, room)}${tag}`}</Text>
          </Button>
        </Box>
      )
    }

    // 選んだノードの詳細は、その行の下に、行のレーンの字下げに合わせた枠で出す。
    // 操作のボタンは枠の最後の行にまとめ、どのノードでも同じ位置に出す。
    const detail = (n: GraphNode, prefix: string) => {
      const links = g.blockers(n.key)
      const canLink = n.ref !== undefined && n.cwd !== undefined
      const actions = [
        canLink && !isPicking && (
          <Button key="add-blocker" onPress={() => void update($, picking, () => true)}>
            + Add blocker
          </Button>
        ),
        isPicking && (
          <Button key="cancel" dimColor onPress={() => void update($, picking, () => false)}>
            Cancel
          </Button>
        ),
        canAdopt(n, self) && (
          <Button key="adopt" onPress={() => void act(() => adopt($, n))}>
            To parking
          </Button>
        ),
      ].filter(Boolean)
      return (
        <Box key={`detail:${n.key}`} flexDirection="column" borderStyle="round" borderDimColor paddingX={1} marginLeft={GUTTER + cells(prefix)}>
          <Text dimColor>
            {n.ref ?? n.key} · {n.session ?? '-'}
            {n.state === 'limit' && n.detail ? ` (再開 ${n.detail})` : ''}
          </Text>
          <Text>blocked by: {chain(g, n)}</Text>
          {links.map(link => blockerRow(n, link))}
          {!canLink && <Text dimColor>辺の端点にできないタスクです (TodoWrite の項目、名前の無いセッション)</Text>}
          {!canSend(n, self) && <Text dimColor>{whyNot(n, self)}</Text>}
          {canSend(n, self) && Input === undefined && <Text dimColor>この画面では指示を入力できません</Text>}
          {isPicking && <Text dimColor>先に済ませるタスクを選ぶ:</Text>}
          {isPicking &&
            candidates(g, n).map(c => (
              <Button key={`blocker:${c.key}`} onPress={() => void act(() => addBlocker($, c, n))}>
                <Text color={COLOR[c.state]}>{ICON[c.state]}</Text>
                {` ${c.title}`}
              </Button>
            ))}
          {canSend(n, self) && Input !== undefined && (
            <Input
              key="instruction"
              label={`${n.session} への指示`}
              placeholder="送る文面"
              submitLabel="Send"
              onSubmit={(text: string) => void send(n, text)}
            />
          )}
          {actions.length > 0 && (
            <Box key={`actions:${n.key}`} flexDirection="row" flexWrap="wrap" gap={1}>
              {actions}
            </Box>
          )}
        </Box>
      )
    }

    // blockerRow は選んだノードを止めている辺 1 本。edge の辺は記録を消せる。同じセッションの blockedBy は daemon から
    // 外せない (TaskUpdate にも外す引数が無い) ので、そのセッションへ解消を頼む。
    const blockerRow = (n: GraphNode, link: GraphLink) => {
      const before = g.node(link.before)
      if (before === undefined) return null
      return (
        <Box key={`blocker-row:${link.before}`} flexDirection="row">
          <Text>
            ← <Text color={COLOR[before.state]}>{ICON[before.state]}</Text> {before.title}{' '}
          </Text>
          {link.kind === 'edge' && link.edge !== undefined && (
            <Button key={`unlink:${link.before}`} onPress={() => void act(() => unlink($, link))}>
              Unlink
            </Button>
          )}
          {link.kind === 'task' && canSend(n, self) && (
            <Button key={`ask:${link.before}`} onPress={() => void act(() => deliver($, n, unlinkText(before, n), 'ask-unlink'))}>
              Ask to unlink → {n.session ?? ''}
            </Button>
          )}
        </Box>
      )
    }

    // メモの行は最後に置かれた時刻 (JST) を先頭に置き、どのメモが古いかを列で見比べられるようにする。
    const memoRow = (memo: ParkingCard) => {
      const key = memoKey(memo)
      const during = memo.sources.flatMap(s => (s.task_id ? (g.node(`${s.session_id}/${s.task_id}`) ?? []) : []))[0]
      const isSel = key === chosen
      return (
        <Box key={`box:${key}`} flexDirection="row">
          <Text>{isSel ? '› ' : ' '.repeat(GUTTER)}</Text>
          <Button key={key} plain onPress={() => choose(isSel ? '' : key)}>
            <Text dimColor>{`${stamp(lastPlacedAt(memo))} `}</Text>
            <Text bold={isSel ? true : undefined}>{`${memoLabel(memo)}${during ? ` ← ${during.title}` : ''}`}</Text>
          </Button>
        </Box>
      )
    }

    // メモの詳細もノードと同じ枠で出し、操作のボタンを枠の最後の行にまとめる。
    const memoDetail = ({ memo, row: memoRowData }: { memo: ParkingCard; row: BoardRow }) => {
      const tasks = taskDestinations(memoRowData.cards, self)
      return (
        <Box key="memo" flexDirection="column" borderStyle="round" borderDimColor paddingX={1} marginLeft={GUTTER}>
          {memoLines(memo).map((line, i) => (
            <Text key={`memo-line-${i}`} dimColor>
              {line}
            </Text>
          ))}
          {tasks.length === 0 && <Text dimColor>この repo に送り先のセッションがありません (タスクにするには同じ repo のセッションが要ります)</Text>}
          <Box key="actions:memo" flexDirection="row" flexWrap="wrap" gap={1}>
            <Button key="discard" onPress={() => void act(() => discard($, memo))}>
              Discard
            </Button>
            {tasks.map(to => (
              <Button key={`task:${to.session_id}`} onPress={() => void act(() => handOff($, memo, to, taskText(memo)))}>
                To task → {to.session}
              </Button>
            ))}
            {issueDestinations(data, memoRowData.cards, self).map(to => (
              <Button key={`issue:${to.session_id}`} onPress={() => void act(() => handOff($, memo, to, issueText(memo, memoRowData.title)))}>
                Ask for issue → {to.session}
              </Button>
            ))}
          </Box>
        </Box>
      )
    }

    return (
      <Box flexDirection="column">
        {failure !== '' && <Text color="red">ボードを読めません: {failure}</Text>}
        <Text dimColor>
          {data.domain === 'home' ? '私用' : '業務'}のボード
          {data.daemon_running ? '' : ' (agents-daemon が動いていないため、セッションの状態は出ません)'}
        </Text>
        <Box flexDirection="row" gap={2}>
          <Text>{summary(g, memos.length)}</Text>
          {g.nodes.some(n => n.state === 'completed') && (
            <Button key="fold" onPress={() => void update($, showCompleted, () => !withCompleted)}>
              {withCompleted ? 'Hide completed' : 'Show completed'}
            </Button>
          )}
        </Box>
        {g.nodes.length === 0 && memos.length === 0 && <Text dimColor>タスクはまだありません</Text>}
        {rows.flatMap((r, i) => {
          const out = [row(r, i)]
          if (r.node === chosen && selected !== undefined) out.push(detail(selected, splitLanes(r.lanes).left))
          return out
        })}
        {memos.length > 0 && (
          <Box key="section:parking" flexDirection="column">
            <Box flexDirection="row" gap={1}>
              <Text bold color="success">
                {MEMO} parking {String(memos.length)}
              </Text>
              {/* 古いメモをまとめて捨てる。該当があるときだけ出し、日数はこの mod が決める (park discard --days)。 */}
              {stale > 0 && (
                <Button key="discard-stale" onPress={() => void act(() => discardStale($, STALE_DAYS))}>
                  Discard {String(stale)} older than {String(STALE_DAYS)}d
                </Button>
              )}
            </Box>
            {memos.map(memo => memoRow(memo))}
            {chosenMemo !== undefined && memoDetail(chosenMemo)}
          </Box>
        )}
        {selected !== undefined && !rows.some(r => r.node === chosen) && <Text dimColor>選んだタスクは今の図に出ていません (完了を畳んでいる)</Text>}
        {sent !== '' && <Text dimColor>{sent}</Text>}
      </Box>
    )
  })
}

// refresh はボードを読み直す (hooks/board-data.ts)。開いたとき (opened) は数をログに書き、それからは数か失敗の理由が
// 変わったときだけ書く。
async function refresh($: EngineInterface, opened = false) {
  const loaded = await loadBoard($)
  if ('failure' in loaded) {
    await logChange($, 'board', 'mod.board.failed', { reason: loaded.failure })
    return
  }
  if (opened) await settle($, 'board')
  await logChange($, 'board', opened ? 'mod.board.open' : 'mod.board.changed', counts(loaded.data))
}

// counts はボードの大きさ。カードの subject とメモの title は書かない。
function counts(data: BoardData): Fields {
  return {
    rows: data.rows.length,
    cards: data.rows.reduce((n, row) => n + row.cards.length, 0),
    memos: data.rows.reduce((n, row) => n + row.parking.length, 0),
    daemon_running: data.daemon_running,
  }
}

// edge は `agents-daemon edge ...` を走らせ、失敗の理由を返す (成功なら空)。辺の domain はバイナリが端点の cwd から決める。
async function edge($: EngineInterface, args: string[]): Promise<string> {
  try {
    const { exitCode, stderr } = await $.process.run([await daemonBin($), 'edge', ...args], { timeoutMs: 10000 })
    return exitCode === 0 ? '' : stderr.trim() || `exit ${exitCode}`
  } catch (err) {
    return String(err)
  }
}

// unlink は辺の元の記録を消す。session: の端点の辺は複数の辺になって描かれるので、まとめて消える。
async function unlink($: EngineInterface, link: GraphLink): Promise<string> {
  const e = link.edge
  if (e === undefined) return ''
  const failure = await edge($, ['rm', '--before', e.before.ref, '--before-cwd', e.before.cwd, '--after', e.after.ref, '--after-cwd', e.after.cwd])
  return failure === '' ? `辺を外しました: ${e.before.ref} → ${e.after.ref}` : `辺を外せませんでした: ${failure}`
}

// addBlocker は before が済まないと after を始められない辺を書く。
async function addBlocker($: EngineInterface, before: GraphNode, after: GraphNode): Promise<string> {
  const failure = await edge($, [
    'add',
    '--before',
    before.ref ?? '',
    '--before-cwd',
    before.cwd ?? '',
    '--after',
    after.ref ?? '',
    '--after-cwd',
    after.cwd ?? '',
  ])
  return failure === '' ? `辺を足しました: ${before.title} → ${after.title}` : `辺を足せませんでした: ${failure}`
}

// deliver はノードのセッションへ text を送り、結果の文面を返す。送った事実だけをログに書く (文面は書かない)。
async function deliver($: EngineInterface, node: GraphNode, text: string, via: string): Promise<string> {
  const to = node.session ?? node.session_id ?? ''
  const event = { via, to: node.session_id ?? '' }
  try {
    const result = await $.session.send({ to: { sessionId: node.session_id ?? '' }, text })
    await logEvent($, 'mod.send', result.isDelivered ? { ...event, delivered: true } : { ...event, delivered: false, reason: result.reason })
    return result.isDelivered ? `${to} へ送りました` : `${to} へ送れませんでした: ${result.reason}`
  } catch (err) {
    await logEvent($, 'mod.send', { ...event, delivered: false, reason: String(err) })
    return `${to} へ送れませんでした: ${String(err)}`
  }
}

// memoKey はメモの Key。ノードの Key (<session_id>/<task_id>) と重ならないよう前置きを付ける。
function memoKey(memo: ParkingCard): string {
  return `park:${memo.id}`
}

function findMemo(rows: BoardRow[], key: string): { memo: ParkingCard; row: BoardRow } | undefined {
  for (const row of rows) {
    const memo = row.parking.find(m => memoKey(m) === key)
    if (memo) return { memo, row }
  }
  return undefined
}

// canSend はノードのセッションへ指示を送れるか。終了したセッション、セッションの無い置き場のノード、自分自身には送らない。
function canSend(node: GraphNode, self: string): boolean {
  return node.live && node.session_id !== undefined && node.session_id !== '' && node.session_id !== self
}

function whyNot(node: GraphNode, self: string): string {
  if (node.session_id === self) return 'このセッションのタスクです'
  if (node.session_id === undefined || node.session_id === '') return 'まだ受け持つセッションがありません'
  return 'このセッションは終了しています'
}

// instruction は送る文面。タスクのノードなら、どのタスクについての指示かを title で添える。
function instruction(node: GraphNode, text: string): string {
  if (node.kind !== 'task') return text
  return `タスク「${node.title}」について: ${text}`
}

// unlinkText は同じセッションの blockedBy の解消を頼む文面。TaskUpdate に blockedBy を外す引数が無いので、
// 待たれている側を片付けるか、待っている側を作り直すよう頼む。
function unlinkText(before: GraphNode, after: GraphNode): string {
  const id = (n: GraphNode) => n.key.slice(n.key.indexOf('/') + 1)
  return [
    `タスク #${id(after)}「${after.title}」が #${id(before)}「${before.title}」の完了待ち (blockedBy) になっています。`,
    `この待ちが要らなければ、#${id(before)} を completed か deleted にするか、#${id(after)} を blockedBy なしで作り直してください。`,
  ].join('')
}
