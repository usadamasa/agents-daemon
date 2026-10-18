// hooks/board.tsx が $.state に置く値の型。形は `agents-daemon status --board` の JSON (internal/board) に合わせる。

export type BoardCard = {
  pane_id: string
  session_id?: string
  cwd: string
  project: string
  agent_status: string
  agent_kind?: string
  message?: string
  wait_until?: string
}

export type BoardLane = {
  id: string
  title: string
  cards: BoardCard[]
}

export type BoardData = {
  daemon_running: boolean
  updated_at?: string
  lanes: BoardLane[]
}

declare module 'claude-code' {
  interface PluginState {
    'agents-daemon': {
      // board は最後に読めたボード。読めていなければ null。
      board: BoardData | null
      // error は直近の読み込みの失敗理由。読めたら空にする。
      error: string
      // target は指示を送る宛先のセッション ID。未選択なら空。
      target: string
      // notice は送信の結果。
      notice: string
    }
  }
}
