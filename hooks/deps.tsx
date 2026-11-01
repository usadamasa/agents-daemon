import { atom, read, update } from 'claude-code'
import type { EngineInterface, Register, Timer } from 'claude-code'

import type { DepsData, DepsRepo, GraphLink, GraphNode, GraphRow, ParkingCard } from '../types'
import { runDaemon } from './daemon-bin'
import type { Requests } from './daemon-bin'
import { loadDeps } from './deps-data'
import { canDismiss, dismissAll, dismissOne } from './dismiss'
import { CHAIN_COLOR, COLOR, Graph, ICON, MEMO, candidates, cells, chain, fit, legendRows, pieces, splitLanes, summary, tags } from './graph'
import { logChange, logEvent, settle } from './log'
import type { Fields } from './log'
import { ask, handOff as handOffTask, handOffToNew, isOwn, startText, statusText, toParking } from './own-task'
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
  peerDestinations,
  SPAWN_SHOWN,
  spawnDestinations,
  spawnLabel,
  spawnText,
  stamp,
  taskDestinations,
  taskText,
} from './parking'
import type { Destination, Intent, SpawnDestination } from './parking'

// 依存図の pane: 依存の DAG (#134) の辺をレーンで描いた行 (graph.rows、上流が上) を dock の pane に描き、選んだノードの
// 詳細と操作、repo ごとのメモの節を出す mod。中身は `agents-daemon deps` (deps パッケージ) の graph と repos (メモと
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

const deps = atom({ plugin: 'agents-daemon', key: 'deps' } as const, null)
const error = atom({ plugin: 'agents-daemon', key: 'error' } as const, '')
// target は選んだノードの Key かメモの Key (park:<id>)。
const target = atom({ plugin: 'agents-daemon', key: 'target' } as const, '')
const notice = atom({ plugin: 'agents-daemon', key: 'notice' } as const, '')
const showCompleted = atom({ plugin: 'agents-daemon', key: 'showCompleted' } as const, false)
const picking = atom({ plugin: 'agents-daemon', key: 'picking' } as const, false)
// OWN_SPAWN は自分のタスクを新しいセッションへ渡す操作の value の前置き。
const OWN_SPAWN = 'own-hand:new'
// spawning は絶対パスの欄を開いた操作 (メモの Intent か、自分のタスクの OWN_SPAWN)。空なら閉じている。
const spawning = atom({ plugin: 'agents-daemon', key: 'spawning' } as const, '' as '' | Intent | typeof OWN_SPAWN)
// moreRepos は起こす repo の候補を SPAWN_SHOWN 件で切らずに全部出すか。
const moreRepos = atom({ plugin: 'agents-daemon', key: 'moreRepos' } as const, false)
const note = atom({ plugin: 'agents-daemon', key: 'note' } as const, '')
const menu = atom({ plugin: 'agents-daemon', key: 'menu' } as const, false)
// dismissing はまとめて外す確かめの最中に、押したときに見えていた行の key。確かめている間に見えた行は消さない。
const dismissing = atom({ plugin: 'agents-daemon', key: 'dismissing' } as const, [] as string[])

// Op は詳細の操作のメニューの 1 行。選んだらすぐ run を走らせる (#308)。value は描画の key。group のある操作
// (宛先を選ぶ Hand off など) は、group の見出しの下に枝で並べ、label には宛先だけを書く。sub があれば group の中で
// もう 1 段、sub の見出しの下に並べる (new session の repo)。同じ group と sub の操作は続けて並べる。
type Op = { value: string; label: string; group?: string; sub?: string; run: () => void }

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
    // mobile には入力欄が無い。依存図は見せて、送信と補足だけ出さない。
    // cc-premise: 2.1.312 mods Input の onInput は打つたびに呼ばれ、同じ value で描き直しても打った字は残る
    // cc-premise: 2.1.312 mods mobile の resolve にも Input があり、置いても描いた木に残らない (claude plugin test で確かめた)。surface で分ける
    const Input = e.surface !== 'mobile' && 'Input' in elements ? elements.Input : undefined
    const data = await read($, deps)
    const failure = await read($, error)
    const chosen = await read($, target)
    const sent = await read($, notice)
    const withCompleted = await read($, showCompleted)
    const isPicking = await read($, picking)
    const isSpawning = await read($, spawning)
    const draft = await read($, note)
    const isMenu = await read($, menu)
    const isMore = await read($, moreRepos)
    const toDismiss = await read($, dismissing)
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
    // 「古い」メモの日数はバイナリが渡す (parking の StaleAfter)。
    const staleDays = data.stale_memo_days
    const stale = memos.filter(memo => isStale(memo, now, staleDays)).length
    const selected = g.node(chosen)
    const chosenMemo = findMemo(data.repos, chosen)
    const rows = withCompleted ? data.graph.rows_with_completed : data.graph.rows
    // ended はまとめて外せる終了したセッションのタスクのうち、いま行に出ているもの (畳んだ完了は数えない)。
    const ended = rows.flatMap(r => {
      const n = r.node === undefined ? undefined : g.node(r.node)
      return n !== undefined && canDismiss(n, self, data) ? [n] : []
    })

    const choose = (key: string) => {
      void update($, target, () => key)
      void update($, notice, () => '')
      void update($, picking, () => false)
      void update($, spawning, () => '')
      void update($, note, () => '')
      void update($, menu, () => false)
      void update($, moreRepos, () => false)
    }
    // act は操作を走らせ、結果を notice に出して依存図を読み直す。
    const act = async (run: () => Promise<string>) => {
      const message = await run()
      await update($, notice, () => message)
      await refresh($)
    }
    // actNoted は補足欄の文を渡して操作を走らせ、欄を空にする。押した時点の下書きを読む。
    const actNoted = (run: (note: string) => Promise<string>) =>
      act(async () => {
        const message = await run(await read($, note))
        await update($, note, () => '')
        return message
      })
    // operations は詳細の操作。横に並べると pane の幅で見切れるので、▾ で開くメニューにまとめ、1 行に 1 つ出す。
    // Select は開いた一覧を後ろの行に重ねて描き、枠の罫線と凡例が崩れるので使わない。枠なしの Button で並べれば
    // 開いた分だけ後ろの行を押し下げる。選んだらメニューを閉じて走らせる。withNote なら ▾ の右に、次に選ぶ操作の
    // 文面へ添える補足欄 (✎) を置く。room は操作の名前を切り詰める幅。
    // cc-premise: 2.1.312 mods Select は開いた一覧を後ろの行に重ねて描き、場所を取らない
    // cc-premise: 2.1.312 mods 空の Input は placeholder が無いと幅を持たず、押して入れない
    const operations = (ops: Op[], withNote: boolean, room: number) => {
      if (ops.length === 0) return null
      return (
        <Box key="ops" flexDirection="column">
          <Box key="ops-row" flexDirection="row" columnGap={1}>
            <Button key="menu" plain onPress={() => void update($, menu, open => !open)}>
              {isMenu ? '▴' : '▾'}
            </Button>
            {withNote && Input !== undefined && <Text dimColor>✎</Text>}
            {withNote && Input !== undefined && (
              <Input
                key="note"
                placeholder="note"
                value={draft}
                submitLabel="keep"
                onInput={(text: string) => void update($, note, () => text)}
                onSubmit={(text: string) => void update($, note, () => text)}
              />
            )}
          </Box>
          {isMenu &&
            ops.flatMap((op, i) => {
              const { group, sub } = op
              const prev = ops[i - 1]
              const next = ops[i + 1]
              const inGroup = (o: Op | undefined) => group !== undefined && o?.group === group
              // subLast は sub の後に同じ group の別の行が無いか。sub の見出しの枝と、その下の行の縦線を決める。
              const subLast = !ops.slice(i + 1).some(o => inGroup(o) && o.sub !== sub)
              const indent = sub === undefined ? '' : subLast ? '  ' : '│ '
              const branch = group === undefined ? '' : (sub === undefined ? inGroup(next) : inGroup(next) && next?.sub === sub) ? '├ ' : '└ '
              const prefix = `${indent}${branch}`
              return [
                group !== undefined && !inGroup(prev) && (
                  <Text key={`group:${group}`} dimColor>
                    {`  ${group}`}
                  </Text>
                ),
                sub !== undefined && !(inGroup(prev) && prev?.sub === sub) && (
                  <Text key={`sub:${group}:${sub}`} dimColor>
                    {`  ${subLast ? '└ ' : '├ '}${sub}`}
                  </Text>
                ),
                <Button
                  key={`op:${op.value}`}
                  plain
                  onPress={() => {
                    void update($, menu, () => false)
                    op.run()
                  }}
                >
                  {`  ${prefix}${fit(op.label, room - 2 - cells(prefix))}`}
                </Button>,
              ]
            })}
        </Box>
      )
    }

    // spawnOps は group の new session の下に並べる、新しいセッションを起こす repo の操作 (#309): 候補を SPAWN_SHOWN 件まで、
    // 残りを出す more…、候補に無い repo の other repo… (絶対パスの欄を開く)。value は `${key}:<repo>` の形。
    const spawnOps = (key: Intent | typeof OWN_SPAWN, group: string, first: { repo: string; title: string } | undefined, run: (to: SpawnDestination) => void): Op[] => {
      const all = spawnDestinations(data, first)
      const shown = isMore ? all : all.slice(0, SPAWN_SHOWN)
      const sub = 'new session'
      return [
        ...shown.map(to => ({ value: `${key}:${destinationKey(to)}`, label: spawnLabel(to, now), group, sub, run: () => run(to) })),
        // 選ぶとメニューは閉じるので、残りを出したら開き直す。
        ...(shown.length < all.length
          ? [{ value: `${key}:more`, label: `more… (${all.length - shown.length})`, group, sub, run: () => void update($, moreRepos, () => true).then(() => update($, menu, () => true)) }]
          : []),
        ...(Input !== undefined ? [{ value: `${key}:path`, label: 'other repo…', group, sub, run: () => void update($, spawning, () => key) }] : []),
      ]
    }

    // pathInput は other repo… で開く絶対パスの欄。相対パスは起こさずに弾いて知らせる。
    const pathInput = (label: string, run: (to: SpawnDestination) => Promise<void>) => {
      if (Input === undefined) return null
      const submit = (path: string) => {
        const to = pathDestination(path)
        if ('failure' in to) return void update($, notice, () => to.failure)
        if (to.route !== 'spawn') return
        void run(to).then(() => update($, spawning, () => ''))
      }
      return (
        <Box key="spawn" flexDirection="column">
          <Input key="spawn-path" placeholder={`/path/to/repo (${label})`} submitLabel="Spawn" onSubmit={submit} />
          <Button key="spawn-cancel" dimColor onPress={() => void update($, spawning, () => '')}>
            Cancel
          </Button>
        </Box>
      )
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
    // 操作のプルダウンは枠の最後の行に置き、どのノードでも同じ位置に出す。
    const detail = (n: GraphNode, prefix: string) => {
      const links = g.blockers(n.key)
      const canLink = n.endpoint && n.cwd !== undefined
      const own = isOwn(n, self)
      const blocker: Op[] = canLink && !isPicking ? [{ value: 'add-blocker', label: '+ Add blocker', run: () => void update($, picking, () => true) }] : []
      // 自分のタスクは TaskUpdate を呼べる自分の model へ頼む (#296)。渡す先は repo を問わず生きているほかのセッションと、
      // mod が新しく起こすセッション (#309)。
      const handToNew = (to: SpawnDestination) => actNoted(said => handOffToNew($, n, to, said))
      const ownOps: Op[] = [
        { value: 'own-start', label: 'Start', run: () => void actNoted(said => ask($, startText(n, said))) },
        { value: 'own-complete', label: 'Complete', run: () => void act(() => ask($, statusText(n, 'completed'))) },
        ...peerDestinations(data, self).map(to => ({
          value: `own-hand:${to.session_id}`,
          label: destinationLabel(to),
          group: 'Hand off',
          run: () => void actNoted(said => handOffTask($, n, to, said)),
        })),
        ...spawnOps(OWN_SPAWN, 'Hand off', undefined, to => void handToNew(to)),
        { value: 'own-delete', label: 'Delete', run: () => void act(() => ask($, statusText(n, 'deleted'))) },
        { value: 'own-park', label: 'To parking', run: () => void act(() => toParking($, n)) },
        ...blocker,
      ]
      const ops: Op[] = own
        ? ownOps
        : [
            ...blocker,
            ...(canAdopt(n, self, data) ? [{ value: 'adopt', label: 'To parking', run: () => void act(() => adopt($, n)) }] : []),
            ...(canDismiss(n, self, data) ? [{ value: 'dismiss', label: 'Dismiss', run: () => void act(() => dismissOne($, n)) }] : []),
          ]
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
          {isPicking && (
            <Button key="cancel" dimColor onPress={() => void update($, picking, () => false)}>
              Cancel
            </Button>
          )}
          {canSend(n, self) && Input !== undefined && (
            <Input
              key="instruction"
              label={`${n.session} への指示`}
              placeholder="送る文面"
              submitLabel="Send"
              onSubmit={(text: string) => void send(n, text)}
            />
          )}
          {/* 枠の中の幅は、字下げと枠と詰めの分 (左右 2 桁ずつ) を引いた残り。 */}
          {operations(ops, own, e.props.bodyColumns - GUTTER - cells(prefix) - 4)}
          {own && isSpawning === OWN_SPAWN && pathInput('Hand off', handToNew)}
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

    // メモの詳細もノードと同じ枠で出し、操作のボタンを枠の最後の行にまとめる。宛先は生きているセッションに続けて、
    // 新しいセッションを起こす repo を並べる (#162)。候補に無い repo は other repo… を選んで絶対パスで指定する。
    const memoDetail = ({ memo, repo }: { memo: ParkingCard; repo: DepsRepo }) => {
      const tasks = taskDestinations(data, repo, self)
      const spawnAt = (to: SpawnDestination, intent: Intent) => actNoted(said => handOff($, memo, to, spawnText(memo, to, intent, said)))
      // handing は intent の操作: 生きているセッションに続けて、新しいセッションを起こす repo (spawnOps)。text は生きている
      // セッションへ送る文面。
      const handing = (intent: Intent, verb: string, sessions: Destination[], text: (said: string) => string): Op[] => [
        ...sessions.map(to => ({
          value: `${intent}:${destinationKey(to)}`,
          label: destinationLabel(to),
          group: verb,
          run: () => void actNoted(said => handOff($, memo, to, text(said))),
        })),
        ...spawnOps(intent, verb, repo, to => void spawnAt(to, intent)),
      ]
      const ops: Op[] = [
        ...handing('task', 'To task', tasks, said => taskText(memo, said)),
        ...handing('issue', 'Ask for issue', issueDestinations(data, repo.sessions, self), said => issueText(memo, repo.title, said)),
        { value: 'discard', label: 'Discard', run: () => void act(() => discard($, memo)) },
      ]
      const intent = isSpawning === 'task' || isSpawning === 'issue' ? isSpawning : undefined
      return (
        <Box key="memo" flexDirection="column" borderStyle="round" borderDimColor paddingX={1} marginLeft={GUTTER}>
          {memoLines(memo).map((line, i) => (
            <Text key={`memo-line-${i}`} dimColor>
              {line}
            </Text>
          ))}
          {tasks.length === 0 && <Text dimColor>この repo に送り先のセッションがありません (タスクにするには同じ repo のセッションが要ります)</Text>}
          {operations(ops, true, e.props.bodyColumns - GUTTER - 4)}
          {intent !== undefined && pathInput(intent === 'task' ? 'To task' : 'Ask for issue', to => spawnAt(to, intent))}
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
                {/* 古いメモをまとめて捨てる。該当があるときだけ出し、日数はバイナリが渡す stale_memo_days (park discard の days)。 */}
                {stale > 0 && (
                  <Button key="discard-stale" onPress={() => void act(() => discardStale($, staleDays))}>
                    Discard {String(stale)} older than {String(staleDays)}d
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
          {/* 一覧の見せ方の操作なので、完了の出し入れと並べて置く。記録は戻せないので、押したら確かめの行に替える。 */}
          {toDismiss.length === 0 && ended.length > 0 && (
            <Button key="dismiss-ended" plain onPress={() => void update($, dismissing, () => ended.map(n => n.key))}>
              <Text dimColor>{`› Dismiss ${ended.length} ended`}</Text>
            </Button>
          )}
          {toDismiss.length > 0 && (
            <Box key="dismiss-confirm-row" flexDirection="row" columnGap={1}>
              <Text>{`${toDismiss.length} つの記録を消します (戻せません)`}</Text>
              <Button
                key="dismiss-confirm"
                onPress={() =>
                  void update($, dismissing, () => []).then(() => act(() => dismissAll($, toDismiss)))
                }
              >
                Dismiss
              </Button>
              <Button key="dismiss-cancel" dimColor onPress={() => void update($, dismissing, () => [])}>
                Cancel
              </Button>
            </Box>
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

// edge は edge add / rm を走らせ、失敗の理由を返す (成功なら空)。
async function edge<C extends 'edge add' | 'edge rm'>($: EngineInterface, command: C, req: Requests[C]): Promise<string> {
  try {
    const { exitCode, stderr } = await runDaemon($, command, req)
    return exitCode === 0 ? '' : stderr.trim() || `exit ${exitCode}`
  } catch (err) {
    return String(err)
  }
}

// unlink は辺の元の記録を消す。session: の端点の辺は複数の辺になって描かれるので、まとめて消える。
async function unlink($: EngineInterface, link: GraphLink): Promise<string> {
  const e = link.edge
  if (e === undefined) return ''
  const failure = await edge($, 'edge rm', { before: { ref: e.before.ref, cwd: e.before.cwd }, after: { ref: e.after.ref, cwd: e.after.cwd } })
  return failure === '' ? `辺を外しました: ${e.before.ref} → ${e.after.ref}` : `辺を外せませんでした: ${failure}`
}

// addBlocker は before が済まないと after を始められない辺を書く。端点はノードの key そのもの。
async function addBlocker($: EngineInterface, before: GraphNode, after: GraphNode): Promise<string> {
  const failure = await edge($, 'edge add', { before: { ref: before.key, cwd: before.cwd ?? '' }, after: { ref: after.key, cwd: after.cwd ?? '' } })
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
