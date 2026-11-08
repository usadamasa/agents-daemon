import type { EngineInterface } from 'claude-code'

import type { DepsRequest, EventLogRequest, ParkAddRequest, ParkListRequest, ParkWithdrawRequest, WorkGetRequest, WorkSetRequest } from '../types'

// daemonBin は hooks/lib/daemon-bin.sh と同じ規則でバイナリの置き場を返す。
export async function daemonBin($: EngineInterface): Promise<string> {
  const cache = (await $.env.get('XDG_CACHE_HOME')) || `${await $.env.get('HOME')}/.cache`

  return `${cache}/agents-daemon/bin/agents-daemon`
}

// Requests は mod が呼ぶコマンドと、その stdin の型 (schema の def)。送達を伴う操作 (park set / discard / adopt / hand、
// edge、task dismiss、spawn、tell) はアプリ (gui/service.go) が呼び、mod は呼ばない。
export type Requests = {
  deps: DepsRequest
  'park add': ParkAddRequest
  'park list': ParkListRequest
  'park withdraw': ParkWithdrawRequest
  'work get': WorkGetRequest
  'work set': WorkSetRequest
  'event-log': EventLogRequest
}

// runDaemon は command を走らせ、req を stdin の JSON で渡す。
export async function runDaemon<C extends keyof Requests>($: EngineInterface, command: C, req: Requests[C], timeoutMs = 10000) {
  return $.process.run([await daemonBin($), ...command.split(' ')], { stdin: JSON.stringify(req), timeoutMs })
}
