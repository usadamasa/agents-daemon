import type { EngineInterface } from 'claude-code'

// daemonBin は hooks/lib/daemon-bin.sh と同じ規則でバイナリの置き場を返す (AGENTS.md「パス定数の対」)。
export async function daemonBin($: EngineInterface): Promise<string> {
  const cache = (await $.env.get('XDG_CACHE_HOME')) || `${await $.env.get('HOME')}/.cache`

  return `${cache}/agents-daemon/bin/agents-daemon`
}
