import { atom, read, update } from 'claude-code'
import type { EngineInterface, Register, Timer } from 'claude-code'

import type { BoardCard, BoardData } from '../types'

// セッションをカードにしたボードを pane に描き、選んだセッションへ指示を送る mod。
// ボードの中身は `agents-daemon status --board` (internal/board) が組み、ここは描くことと送ることだけを持つ。
// cc-premise: 2.1.302 mods pane / clock.every / process.run / session.send の API で試作した

const PANE = 'agents-daemon-board'
const TITLE = 'Sessions'
// daemon は tick ごとに status.json を書き直す。それより細かく読んでも変わらないので数秒おきにする。
const POLL_MS = 3000

const board = atom({ plugin: 'agents-daemon', key: 'board' } as const, null)
const error = atom({ plugin: 'agents-daemon', key: 'error' } as const, '')
const target = atom({ plugin: 'agents-daemon', key: 'target' } as const, '')
const notice = atom({ plugin: 'agents-daemon', key: 'notice' } as const, '')

// timer はボードの読み直し。pane を開いている間だけ動かす。
let timer: Timer | undefined

export const register: Register = on => {
  on('session.start', async ($, e, next) => {
    await $.command.register({
      name: 'board',
      description: 'agents-daemon が見ているセッションをボードで表示し、選んだセッションへ指示を送る',
    })

    return next(e)
  })

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
    // 宛先はボードに載っているセッションからしか選べない。自由入力にすると、同じアカウントの
    // 別マシンのセッションへも届きうるため (issue #121 の「操作」の制限)。
    const cards = data?.lanes.flatMap(lane => lane.cards) ?? []
    const chosenCard = cards.find(card => card.session_id === chosen)

    const choose = (card: BoardCard) => {
      void update($, target, () => card.session_id ?? '')
      void update($, notice, () => '')
    }
    const send = async (text: string) => {
      if (!chosenCard?.session_id || text.trim() === '') return
      try {
        const result = await $.session.send({ to: { sessionId: chosenCard.session_id }, text })
        await update($, notice, () =>
          result.isDelivered
            ? `${chosenCard.project} へ送りました`
            : `${chosenCard.project} へ送れませんでした: ${result.reason}`,
        )
      } catch (err) {
        await update($, notice, () => `${chosenCard.project} へ送れませんでした: ${String(err)}`)
      }
    }

    return (
      <Box flexDirection="column">
        {failure !== '' && <Text color="red">board を読めません: {failure}</Text>}
        {data === null && failure === '' && <Text dimColor>読み込み中…</Text>}
        {data !== null && !data.daemon_running && <Text dimColor>agents-daemon が動いていません</Text>}
        {data !== null && data.daemon_running && data.updated_at === undefined && (
          <Text dimColor>daemon の最初の tick を待っています</Text>
        )}
        {data !== null &&
          data.lanes.map(lane => (
            <Box key={lane.id} flexDirection="column">
              <Text bold>
                {lane.title} ({lane.cards.length})
              </Text>
              {lane.cards.map(card => (
                <Button
                  key={card.pane_id}
                  label={cardLabel(card, self)}
                  variant={card.session_id !== undefined && card.session_id === chosen ? 'primary' : 'secondary'}
                  dimColor={card.session_id === undefined || card.session_id === self}
                  onPress={() => choose(card)}
                />
              ))}
            </Box>
          ))}
        {chosenCard !== undefined && chosenCard.session_id !== self && Input === undefined && (
          <Text dimColor>この画面では指示を入力できません</Text>
        )}
        {chosenCard !== undefined && chosenCard.session_id !== self && Input !== undefined && (
          <Input
            key="instruction"
            label={`${chosenCard.project} への指示`}
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
async function refresh($: EngineInterface) {
  try {
    const { exitCode, stdout, stderr } = await $.process.run([await daemonBin($), 'status', '--board'], { timeoutMs: 10000 })
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

// daemonBin は hooks/lib/daemon-bin.sh と同じ規則でバイナリの置き場を返す (AGENTS.md「パス定数の対」)。
async function daemonBin($: EngineInterface): Promise<string> {
  const cache = (await $.env.get('XDG_CACHE_HOME')) || `${await $.env.get('HOME')}/.cache`

  return `${cache}/agents-daemon/bin/agents-daemon`
}

// cardLabel はカード 1 枚の 1 行。指示を送れないもの (自分自身、セッション ID が無い) はそう書く。
function cardLabel(card: BoardCard, self: string): string {
  const parts = [card.project || card.pane_id]
  if (card.agent_kind) parts.push(`[${card.agent_kind}]`)
  if (card.message) parts.push(card.message)
  if (card.wait_until) parts.push(`再開 ${card.wait_until}`)
  if (card.session_id === self) parts.push('(このセッション)')
  else if (card.session_id === undefined) parts.push('(セッション不明)')

  return parts.join(' ')
}
