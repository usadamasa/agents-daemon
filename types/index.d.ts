// hooks/board.tsx が $.state に置く値の型。形は `agents-daemon status --board` の JSON (internal/board) に合わせる。

export type BoardColumn = {
  id: 'pending' | 'in_progress' | 'blocked' | 'completed'
  title: string
}

export type BoardCard = {
  key: string
  kind: 'task' | 'todo' | 'session'
  column: BoardColumn['id']
  subject: string
  session_id: string
  session: string
  live: boolean
  blockers?: number
  mark?: 'input' | 'limit'
  mark_detail?: string
  updated_at: string
}

export type ParkingSource = {
  session_id: string
  session: string
  live: boolean
  detail?: string
  at: string
}

export type ParkingCard = {
  id: string
  kind: 'aside' | 'scope' | 'preference' | 'task'
  title: string
  detail?: string
  files?: string[]
  default_taken?: string
  sources: ParkingSource[]
}

export type BoardRow = {
  repo: string
  title: string
  cards: BoardCard[]
  parking: ParkingCard[]
}

export type BoardData = {
  domain: 'home' | 'work'
  daemon_running: boolean
  updated_at?: string
  columns: BoardColumn[]
  rows: BoardRow[]
}

declare module 'claude-code' {
  interface PluginState {
    'agents-daemon': {
      // board は最後に読めたボード。読めていなければ null。
      board: BoardData | null
      // error は直近の読み込みの失敗理由。読めたら空にする。
      error: string
      // target は指示を送るカードの key。未選択なら空。
      target: string
      // notice は送信の結果。
      notice: string
    }
  }
}
