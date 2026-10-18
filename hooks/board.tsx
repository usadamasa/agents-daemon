import { atom, read, update } from 'claude-code'
import type { EngineInterface, Register, Timer } from 'claude-code'

import type { BoardCard, BoardData } from '../types'
import { daemonBin } from './daemon-bin'

// タスクのボード (#121) を pane に描き、選んだタスクのセッションへ指示を送る mod。
// 行は repo、列はタスクの状態で、ボードの中身は `agents-daemon status --board` (internal/board) が組む。
// ここは描くことと送ることだけを持つ。
// cc-premise: 2.1.302 mods pane / clock.every / process.run / session.send / session.cwd の API で書いた

const PANE = 'agents-daemon-board'
const TITLE = 'Tasks'
// daemon は tick ごとに status.json を書き直し、タスクの記録は Task 系ツールのたびに変わる。
// それより細かく読んでも変わらないので数秒おきにする。
const POLL_MS = 3000

const board = atom({ plugin: 'agents-daemon', key: 'board' } as const, null)
const error = atom({ plugin: 'agents-daemon', key: 'error' } as const, '')
const target = atom({ plugin: 'agents-daemon', key: 'target' } as const, '')
const notice = atom({ plugin: 'agents-daemon', key: 'notice' } as const, '')

// timer はボードの読み直し。pane を開いている間だけ動かす。
let timer: Timer | undefined

// startBoard は session.start で /board を登録する (session.start の hook は hooks/register.ts が持つ)。
export async function startBoard($: EngineInterface) {
  await $.command.register({
    name: 'board',
    description: 'agents-daemon のタスクのボードを表示し、選んだタスクのセッションへ指示を送る',
  })
}

export const registerBoard: Register = on => {
  on('command.run', { command: 'board' }, async $ => {
    await refresh($)
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
    // mobile には入力欄が無い。ボードは見せて、送信だけ出さない。
    const Input = 'Input' in elements ? elements.Input : undefined
    const data = await read($, board)
    const failure = await read($, error)
    const chosen = await read($, target)
    const sent = await read($, notice)
    const self = await $.session.id()
    // 宛先はボードに載っているカードのセッションからしか選べない。自由入力にすると、別の domain や
    // 同じアカウントの別マシンのセッションへも届きうるため (#121 の「送信」の制限)。
    const cards = data?.rows.flatMap(row => row.cards) ?? []
    const chosenCard = cards.find(card => card.key === chosen)
    const sendable = chosenCard !== undefined && canSend(chosenCard, self)

    const choose = (card: BoardCard) => {
      void update($, target, () => card.key)
      void update($, notice, () => '')
    }
    const send = async (text: string) => {
      if (!chosenCard || !canSend(chosenCard, self) || text.trim() === '') return
      const to = `${chosenCard.session}`
      try {
        const result = await $.session.send({ to: { sessionId: chosenCard.session_id }, text: instruction(chosenCard, text) })
        await update($, notice, () => (result.isDelivered ? `${to} へ送りました` : `${to} へ送れませんでした: ${result.reason}`))
      } catch (err) {
        await update($, notice, () => `${to} へ送れませんでした: ${String(err)}`)
      }
    }

    return (
      <Box flexDirection="column">
        {failure !== '' && <Text color="red">ボードを読めません: {failure}</Text>}
        {data === null && failure === '' && <Text dimColor>読み込み中…</Text>}
        {data !== null && (
          <Text dimColor>
            {data.domain === 'home' ? '私用' : '業務'}のボード
            {data.daemon_running ? '' : ' (agents-daemon が動いていないため、セッションの状態は出ません)'}
          </Text>
        )}
        {data !== null && data.rows.length === 0 && <Text dimColor>タスクはまだありません</Text>}
        {data !== null &&
          data.rows.map(row => (
            <Box key={row.repo} flexDirection="column">
              <Text bold>{row.title}</Text>
              {data.columns.map(column => {
                const inColumn = row.cards.filter(card => card.column === column.id)
                if (inColumn.length === 0) return null
                return (
                  <Box key={`${row.repo}:${column.id}`} flexDirection="column">
                    <Text dimColor>
                      {column.title} ({inColumn.length})
                    </Text>
                    {inColumn.map(card => (
                      <Button
                        key={card.key}
                        label={cardLabel(card, self)}
                        variant={card.key === chosen ? 'primary' : 'secondary'}
                        dimColor={!canSend(card, self)}
                        onPress={() => choose(card)}
                      />
                    ))}
                  </Box>
                )
              })}
            </Box>
          ))}
        {chosenCard !== undefined && !sendable && <Text dimColor>{whyNot(chosenCard, self)}</Text>}
        {sendable && Input === undefined && <Text dimColor>この画面では指示を入力できません</Text>}
        {sendable && Input !== undefined && (
          <Input
            key="instruction"
            label={`${chosenCard.session} への指示`}
            placeholder="送る文面"
            submitLabel="送る"
            onSubmit={(text: string) => void send(text)}
          />
        )}
        {sent !== '' && <Text dimColor>{sent}</Text>}
      </Box>
    )
  })
}

// refresh は `agents-daemon status --board` を走らせてボードを読み直す。失敗は error に置き、前のボードは残す。
// 開いたセッションの ID と cwd を渡し、そのセッションの domain だけを描かせる (私用と業務を混ぜない)。
async function refresh($: EngineInterface) {
  try {
    const argv = [await daemonBin($), 'status', '--board', '--session', await $.session.id(), '--cwd', await $.session.cwd()]
    const { exitCode, stdout, stderr } = await $.process.run(argv, { timeoutMs: 10000 })
    if (exitCode !== 0) {
      await update($, error, () => stderr.trim() || `exit ${exitCode}`)
      return
    }
    const data = JSON.parse(stdout) as BoardData
    await update($, board, () => data)
    await update($, error, () => '')
  } catch (err) {
    await update($, error, () => String(err))
  }
}

// canSend はカードのセッションへ指示を送れるか。終了したセッションと自分自身には送らない。
function canSend(card: BoardCard, self: string): boolean {
  return card.live && card.session_id !== '' && card.session_id !== self
}

function whyNot(card: BoardCard, self: string): string {
  if (card.session_id === self) return 'このセッションのタスクです'
  return 'このセッションは終了しています'
}

// instruction は送る文面。タスクのカードなら、どのタスクについての指示かを subject で添える。
function instruction(card: BoardCard, text: string): string {
  if (card.kind === 'session') return text
  return `タスク「${card.subject}」について: ${text}`
}

// cardLabel はカード 1 枚の 1 行: subject、セッション、ブロックの数、セッションの状態の印。
function cardLabel(card: BoardCard, self: string): string {
  const parts = [card.subject, `· ${card.session}`]
  if (card.blockers) parts.push(`←${card.blockers}`)
  if (card.mark === 'input') parts.push(`⚠入力待ち${card.mark_detail ? ` (${card.mark_detail})` : ''}`)
  if (card.mark === 'limit') parts.push(`⏳上限待ち${card.mark_detail ? ` (再開 ${card.mark_detail})` : ''}`)
  if (card.session_id === self) parts.push('(このセッション)')
  else if (!card.live) parts.push('(終了)')

  return parts.join(' ')
}
