import type { EngineInterface, Register } from 'claude-code'

import { logEvent } from './log'

// 依存図のアプリ (agent-tasks) を開く。対話のセッションが始まったら前面を奪わずに開き、閉じた後は /agent-tasks で開き直す。
// Bash の sandbox は open (Launch Services) を止めるので、mod の process.run から打つ。open は起動中のアプリには
// 何もしない (-g なら前面にも出さない) ので、開いているかは確かめない。
// cc-premise: 2.1.302 mods command.register / command.run の API で書いた

const COMMAND = 'agent-tasks'
const OPEN = '/usr/bin/open'

// guiApp は hooks/lib/daemon-bin.sh の GUI_APP と同じ置き場を返す。
async function guiApp($: EngineInterface): Promise<string> {
  return `${await $.env.get('HOME')}/Applications/agent-tasks.app`
}

// open は agent-tasks を開き、開けなければ理由を返す (開けたら空)。background は前面を奪わない。
async function open($: EngineInterface, background: boolean): Promise<string> {
  const app = await guiApp($)
  try {
    const res = await $.process.run(background ? [OPEN, '-g', app] : [OPEN, app], { timeoutMs: 5000 })
    return res.exitCode === 0 ? '' : res.stderr.trim() || `exit ${res.exitCode}`
  } catch (err) {
    return String(err)
  }
}

// openOnStart は session.start で呼ぶ (session.start の hook は hooks/register.ts が持つ)。失敗はログに残すだけで、
// セッションの開始は止めない (macOS 以外やアプリがまだ建っていないときもここ)。
export async function openOnStart($: EngineInterface, isInteractive: boolean) {
  // AGENTS_DAEMON_OPEN_APP=0 で開かない (既定は開く)。
  if (!isInteractive || (await $.env.get('AGENTS_DAEMON_OPEN_APP')) === '0') return
  const reason = await open($, true)
  if (reason) await logEvent($, 'mod.open-app.failed', { reason })
}

// startOpenApp は session.start で /agent-tasks を登録する。
export async function startOpenApp($: EngineInterface) {
  await $.command.register({ name: COMMAND, description: '依存図のアプリ agent-tasks を開く', immediate: true })
}

export const registerOpenApp: Register = on => {
  on('command.run', { command: 'agent-tasks' }, async $ => {
    const reason = await open($, false)
    if (!reason) return { text: 'agent-tasks を開きました' }
    return {
      text: `agent-tasks を開けません: ${reason}\nアプリは SessionStart hook が建てます。建っていなければ ~/.local/state/agents-daemon/logs/build.log を見てください。`,
    }
  })
}
