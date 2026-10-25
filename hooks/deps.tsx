import { atom, read, update } from 'claude-code'
import type { EngineInterface, Register, Timer } from 'claude-code'

import type { DepsData, DepsRepo, GraphLink, GraphNode, GraphRow, ParkingCard } from '../types'
import { daemonBin } from './daemon-bin'
import { loadDeps } from './deps-data'
import { CHAIN_COLOR, COLOR, Graph, ICON, MEMO, candidates, cells, chain, fit, legendRows, pieces, splitLanes, summary, tags } from './graph'
import { logChange, logEvent, settle } from './log'
import type { Fields } from './log'
import {
  adopt,
  canAdopt,
  destinationKey,
  destinationLabel,
  discard,
  discardStale,
  handOff,
  isStale,
  issueDestinations,
  issueText,
  lastPlacedAt,
  memoLabel,
  memoLines,
  pathDestination,
  spawnDestinations,
  spawnText,
  stamp,
  taskDestinations,
  taskText,
} from './parking'
import type { Destination, Intent } from './parking'

// 依存図の pane: 依存の DAG (#134) の辺をレーンで描いた行 (graph.rows、上流が上) を dock の pane に描き、選んだノードの
// 詳細と操作、repo ごとのメモの節を出す mod。中身は `agents-daemon deps` (internal/deps) の graph と repos (メモと
// その送り先)。ここは描くことと送ることだけを持つ。
// pane は会話の横 (dock) に置かれるときだけ出す。入力欄の上 (inline) には出さない。
// cc-premise: 2.1.303 mods pane の placement (dock / inline)、command.run の presentation.isFullscreen、ui.close
// cc-premise: 2.1.302 mods pane / clock.every / process.run / session.send / session.cwd の API で書いた
// cc-premise: 2.1.303 mods Button の plain は端末で [ ] を描かず、variant より優先される。行は plain、操作は枠付き

const PANE = 'agents-daemon-deps'
const TITLE = 'Tasks'
// daemon は tick ごとに status.json を書き直し、タスクの記録は Task 系ツールのたびに変わる。
// それより細かく読んでも変わらないので数秒おきにする。
const POLL_MS = 3000
// 行頭の印。選んだ行だけに › を出し、ほかの行は同じ幅の空白で揃える。
const GUTTER = 2
// STALE_DAYS は「古い」メモの日数。週次の棚卸しを 4 回越したメモは文脈が薄れている。daemon の housekeeping は open を
// 90 日で黙って消す (internal/parking の OpenRetention) が、その前に人が見て捨てる区切りをここに置く。
const STALE_DAYS = 30

const deps = atom({ plugin: 'agents-daemon', key: 'deps' } as const, null)
const error = atom({ plugin: 'agents-daemon', key: 'error' } as const, '')
// target は選んだノードの Key かメモの Key (park:<id>)。
const target = atom({ plugin: 'agents-daemon', key: 'target' } as const, '')
const notice = atom({ plugin: 'agents-daemon', key: 'notice' } as const, '')
const showCompleted = atom({ plugin: 'agents-daemon', key: 'showCompleted' } as const, false)
const picking = atom({ plugin: 'agents-daemon', key: 'picking' } as const, false)
const spawning = atom({ plugin: 'agents-daemon', key: 'spawning' } as const, '' as '' | Intent)

// timer は依存図の読み直し。pane を開いている間だけ動かす。
let timer: Timer | undefined
// closing は inline に描かれて閉じにかかったか。閉じるまでに描画がもう一度走っても、知らせを重ねない。
let closing = false

// startDeps は session.start で /deps を登録する (session.start の hook は hooks/register.ts が持つ)。
export async function startDeps($: EngineInterface) {
  await $.command.register({
    name: 'deps',
    description: 'agents-daemon の依存図を会話の横に開き、選んだタスクのセッションへ指示を送る',
  })
}

export const registerDeps: Register = on => {
  on('command.run', { command: 'deps' }, async ($, e) => {
    if (!e.presentation.isFullscreen) {
      return { text: '依存図は fullscreen のレイアウトで、会話の横 (dock) にだけ開きます。この画面では開きません。' }
    }
    closing = false
    await refresh($, true)
    timer?.cancel()
    timer = $.clock.every(POLL_MS, () => void refresh($))
    await $.ui.open({ id: PANE, title: TITLE })

    return { text: 'agents-daemon の依存図を開きました。' }
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
    // 端末を狭めて入力欄の上に置かれたら閉じる。
    if (e.props.placement === 'inline') {
      if (!closing) {
        closing = true
        await $.ui.close({ id: PANE })
        // 閉じた pane の中の文面は誰にも見えないので、toast で知らせる。
        await $.ui.toast('会話の横に置けないため、依存図を閉じました (端末を 110 桁以上にすると開けます)')
      }
      return <Text dimColor>会話の横に置けないため、依存図を閉じました。</Text>
    }
    // mobile には入力欄が無い。依存図は見せて、送信だけ出さない。
    const Input = 'Input' in elements ? elements.Input : undefined
    const data = await read($, deps)
    const failure = await read($, error)
    const chosen = await read($, target)
    const sent = await read($, notice)
    const withCompleted = await read($, showCompleted)
    const isPicking = await read($, picking)
    const isSpawning = await read($, spawning)
    const self = await $.session.id()

    if (data === null) {
      return (
        <Box flexDirection="column">
          {failure !== '' && <Text color="red">依存図を読めません: {failure}</Text>}
          {failure === '' && <Text dimColor>読み込み中…</Text>}
        </Box>
      )
    }

    const g = new Graph(data.graph)
    const memos = data.repos.flatMap(repo => repo.memos)
    const now = await $.clock.now()
    const stale = memos.filter(memo => isStale(memo, now, STALE_DAYS)).length
    const selected = g.node(chosen)
    const chosenMemo = findMemo(data.repos, chosen)
    const rows = withCompleted ? data.graph.rows_with_completed : data.graph.rows

    const choose = (key: string) => {
      void update($, target, () => key)
      void update($, notice, () => '')
      void update($, picking, () => false)
      void update($, spawning, () => '')
    }
    // act は操作を走らせ、結果を notice に出して依存図を読み直す。
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
    // lanes は罫線の区間を描く。longest chain の辺の桁 (marks) だけ色を付け、ほかは薄く描く。
    const lanes = (r: GraphRow, from: number, to: number) =>
      pieces(r.lanes, r.marks, from, to).map((p, k) =>
        p.marked ? (
          <Text key={String(k)} color={CHAIN_COLOR}>
            {p.text}
          </Text>
        ) : (
          <Text key={String(k)} dimColor>
            {p.text}
          </Text>
        ),
      )
    const row = (r: GraphRow, i: number) => {
      const { left, right } = splitLanes(r.lanes)
      const n = r.node === undefined ? undefined : g.node(r.node)
      if (n === undefined) {
        return (
          <Box key={`box:lane:${i}`} flexDirection="row">
            <Text>{' '.repeat(GUTTER)}</Text>
            {lanes(r, 0, left.length)}
          </Box>
        )
      }
      const isSel = n.key === chosen
      const tag = tags(n, n.stuck)
      // 行頭の印、レーン、記号と空白を除いた幅に title を収める。
      const room = Math.max(4, e.props.bodyColumns - GUTTER - cells(left) - 1 - cells(right) - 1 - cells(tag))
      return (
        <Box key={`box:row:${n.key}`} flexDirection="row">
          <Text>{isSel ? '› ' : ' '.repeat(GUTTER)}</Text>
          {lanes(r, 0, left.length)}
          <Button key={`row:${n.key}`} plain onPress={() => choose(isSel ? '' : n.key)}>
            <Text color={COLOR[n.state]}>{ICON[n.state]}</Text>
            {lanes(r, left.length + 1, r.lanes.length)}
            <Text color={n.halted ? COLOR[n.state] : undefined} bold={isSel ? true : undefined}>{` ${fit(n.title, room)}${tag}`}</Text>
          </Button>
        </Box>
      )
    }

    // 選んだノードの詳細は、その行の下に、行のレーンの字下げに合わせた枠で出す。
    // 操作のボタンは枠の最後の行にまとめ、どのノードでも同じ位置に出す。
    const detail = (n: GraphNode, prefix: string) => {
      const links = g.blockers(n.key)
      const canLink = n.endpoint && n.cwd !== undefined
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
        canAdopt(n, self, data) && (
          <Button key="adopt" onPress={() => void act(() => adopt($, n))}>
            To parking
          </Button>
        ),
      ].filter(Boolean)
      return (
        <Box key={`detail:${n.key}`} flexDirection="column" borderStyle="round" borderDimColor paddingX={1} marginLeft={GUTTER + cells(prefix)}>
          <Text dimColor>
            {heading(n)}
            {n.state === 'limit' && n.detail ? ` (再開 ${n.detail})` : ''}
          </Text>
          <Text>blocked by: {chain(g, n)}</Text>
          {n.memos.map(m => (
            <Text key={`memo:${m.id}`}>{`◇ ${m.title} (${m.status})`}</Text>
          ))}
          {links.map(link => blockerRow(n, link))}
          {!canLink && <Text dimColor>辺の端点にできないタスクです (TodoWrite の項目)</Text>}
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
      const during = memo.sources.flatMap(s => (s.task_ref ? (g.node(s.task_ref) ?? []) : []))[0]
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

    // メモの詳細もノードと同じ枠で出し、操作のボタンを枠の最後の行にまとめる。生きているセッションの無い repo へは
    // 「new session…」で repo の候補を開き、新しいセッションを起こして渡す (#162)。
    const memoDetail = ({ memo, repo }: { memo: ParkingCard; repo: DepsRepo }) => {
      const tasks = taskDestinations(data, repo, self)
      // spawn は候補の repo で新しいセッションを起こし、渡したら候補を畳む。
      const spawn = async (to: Destination, intent: Intent) => {
        if (to.route !== 'spawn') return
        await act(() => handOff($, memo, to, spawnText(memo, to, intent)))
        await update($, spawning, () => '')
      }
      const spawnAt = (path: string, intent: Intent) => {
        const to = pathDestination(path)
        if ('failure' in to) return void update($, notice, () => to.failure)
        void spawn(to, intent)
      }
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
              <Button key={`task:${destinationKey(to)}`} onPress={() => void act(() => handOff($, memo, to, taskText(memo)))}>
                To task → {destinationLabel(to)}
              </Button>
            ))}
            <Button key="spawn-task" onPress={() => void update($, spawning, () => 'task')}>
              To task → new session…
            </Button>
            {issueDestinations(data, repo.sessions, self).map(to => (
              <Button key={`issue:${destinationKey(to)}`} onPress={() => void act(() => handOff($, memo, to, issueText(memo, repo.title)))}>
                Ask for issue → {destinationLabel(to)}
              </Button>
            ))}
            <Button key="spawn-issue" onPress={() => void update($, spawning, () => 'issue')}>
              Ask for issue → new session…
            </Button>
          </Box>
          {isSpawning !== '' && (
            <Box key="spawn" flexDirection="column">
              <Text dimColor>{isSpawning === 'task' ? 'タスクにする' : 'issue にするよう頼む'}新しいセッションを起こす repo を選ぶ:</Text>
              <Box flexDirection="row" flexWrap="wrap" gap={1}>
                {spawnDestinations(data, repo).map(to => (
                  <Button key={`spawn:${destinationKey(to)}`} onPress={() => void spawn(to, isSpawning)}>
                    {destinationLabel(to)}
                  </Button>
                ))}
                <Button key="spawn-cancel" dimColor onPress={() => void update($, spawning, () => '')}>
                  Cancel
                </Button>
              </Box>
              {Input !== undefined && (
                <Input key="spawn-path" label="別の repo の絶対パス" placeholder="/path/to/repo" submitLabel="Spawn" onSubmit={(text: string) => spawnAt(text, isSpawning)} />
              )}
            </Box>
          )}
        </Box>
      )
    }

    // root は中身と凡例の 2 つだけを持つ。pane の高さに満たないときは凡例を底に寄せ、はみ出すときは中身の後に続く。
    return (
      <Box key="root" flexDirection="column" minHeight={e.props.scroll.bodyRows} justifyContent="space-between">
        <Box key="content" flexDirection="column">
          {failure !== '' && <Text color="red">依存図を読めません: {failure}</Text>}
          {!data.daemon_running && <Text dimColor>agents-daemon が動いていないため、セッションの状態は出ません</Text>}
          <Text>{summary(g, memos.length)}</Text>
          {g.nodes.length === 0 && memos.length === 0 && <Text dimColor>タスクはまだありません</Text>}
          {rows.flatMap((r, i) => {
            const out = [row(r, i)]
            if (r.node === chosen && selected !== undefined) out.push(detail(selected, splitLanes(r.lanes).left))
            return out
          })}
          {memos.length > 0 && (
            <Box key="section:parking" flexDirection="column">
              <Box flexDirection="row" gap={1}>
                <Text bold>
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
        {/* 凡例は詳細と同じ枠で囲む。枠と詰めの分 (左右 2 桁ずつ) を引いた幅で行を組む。完了の出し入れは
            図の見せ方の切り替えなので、数の行に置かず凡例の枠の最後に枠なしで置く。 */}
        <Box key="legend" flexDirection="column" borderStyle="round" borderDimColor paddingX={1} marginTop={1}>
          {legendRows(e.props.bodyColumns - 4).map((line, i) => (
            <Box key={`legend:${i}`}>
              <Text dimColor>{line}</Text>
            </Box>
          ))}
          {g.nodes.some(n => n.state === 'completed') && (
            <Button key="fold" plain onPress={() => void update($, showCompleted, () => !withCompleted)}>
              <Text dimColor>{withCompleted ? '› Hide completed' : '› Show completed'}</Text>
            </Button>
          )}
        </Box>
      </Box>
    )
  })
}

// refresh は依存図を読み直す (hooks/deps-data.ts)。開いたとき (opened) は数をログに書き、それからは数か失敗の理由が
// 変わったときだけ書く。
async function refresh($: EngineInterface, opened = false) {
  const loaded = await loadDeps($)
  if ('failure' in loaded) {
    await logChange($, 'deps', 'mod.deps.failed', { reason: loaded.failure })
    return
  }
  if (opened) await settle($, 'deps')
  await logChange($, 'deps', opened ? 'mod.deps.open' : 'mod.deps.changed', counts(loaded.data))
}

// counts は依存図の大きさ。タスクの title とメモの title は書かない。
function counts(data: DepsData): Fields {
  return {
    repos: data.repos.length,
    sessions: data.repos.reduce((n, repo) => n + repo.sessions.length, 0),
    nodes: data.graph.nodes.length,
    memos: data.repos.reduce((n, repo) => n + repo.memos.length, 0),
    daemon_running: data.daemon_running,
  }
}

// edge は `agents-daemon edge ...` を走らせ、失敗の理由を返す (成功なら空)。
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

// addBlocker は before が済まないと after を始められない辺を書く。端点はノードの key そのもの。
async function addBlocker($: EngineInterface, before: GraphNode, after: GraphNode): Promise<string> {
  const failure = await edge($, ['add', '--before', before.key, '--before-cwd', before.cwd ?? '', '--after', after.key, '--after-cwd', after.cwd ?? ''])
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

// memoKey は pane でメモを選ぶ key。ノードの key (Ref) の種類に無い前置き park: を付けて重ならないようにする。
function memoKey(memo: ParkingCard): string {
  return `park:${memo.id}`
}

function findMemo(repos: DepsRepo[], key: string): { memo: ParkingCard; repo: DepsRepo } | undefined {
  for (const repo of repos) {
    const memo = repo.memos.find(m => memoKey(m) === key)
    if (memo) return { memo, repo }
  }
  return undefined
}

// heading は詳細の 1 行目。タスクの無いセッションのノードは key の代わりに名前を出し、同名のセッションを
// 見分けられるよう session ID の先頭を添える。
function heading(n: GraphNode): string {
  if (n.kind === 'session') return `${n.session ?? '-'} · ${(n.session_id ?? '').slice(0, 8)}`
  return `${n.key} · ${n.session ?? '-'}`
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
  const id = (n: GraphNode) => n.task_id ?? ''
  return [
    `タスク #${id(after)}「${after.title}」が #${id(before)}「${before.title}」の完了待ち (blockedBy) になっています。`,
    `この待ちが要らなければ、#${id(before)} を completed か deleted にするか、#${id(after)} を blockedBy なしで作り直してください。`,
  ].join('')
}
