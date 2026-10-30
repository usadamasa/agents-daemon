import type { EngineInterface } from 'claude-code'

import type {
  DepsRequest,
  EdgeAddRequest,
  EdgeRemoveRequest,
  EventLogRequest,
  ParkAddRequest,
  ParkAdoptRequest,
  ParkDiscardRequest,
  ParkListRequest,
  ParkSetRequest,
  SpawnRequest,
} from '../types'

// daemonBin は hooks/lib/daemon-bin.sh と同じ規則でバイナリの置き場を返す。
export async function daemonBin($: EngineInterface): Promise<string> {
  const cache = (await $.env.get('XDG_CACHE_HOME')) || `${await $.env.get('HOME')}/.cache`

  return `${cache}/agents-daemon/bin/agents-daemon`
}

// Requests は mod が呼ぶコマンドと、その stdin の型 (schema の def)。
export type Requests = {
  deps: DepsRequest
  'park add': ParkAddRequest
  'park set': ParkSetRequest
  'park list': ParkListRequest
  'park discard': ParkDiscardRequest
  'park adopt': ParkAdoptRequest
  'edge add': EdgeAddRequest
  'edge rm': EdgeRemoveRequest
  spawn: SpawnRequest
  'event-log': EventLogRequest
}

// runDaemon は command を走らせ、req を stdin の JSON で渡す。
export async function runDaemon<C extends keyof Requests>($: EngineInterface, command: C, req: Requests[C], timeoutMs = 10000) {
  return $.process.run([await daemonBin($), ...command.split(' ')], { stdin: JSON.stringify(req), timeoutMs })
}
