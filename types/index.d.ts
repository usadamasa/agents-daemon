// hooks/board.tsx、hooks/park.tsx、hooks/band.tsx が $.state に置く値の型。形は `agents-daemon status --board` の JSON (internal/board) に合わせる。

export type BoardColumn = {
  id: 'pending' | 'in_progress' | 'blocked' | 'completed'
  title: string
}

export type BoardCard = {
  key: string
  kind: 'task' | 'todo' | 'session'
  task_id?: string
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

// ParkedMemo は `park list` の 1 件を band と hint 行に出す分だけにしたもの。at は最後に置かれた時刻。
export type ParkedMemo = {
  id: string
  kind: ParkingCard['kind']
  title: string
  at: string
}

// Band は入力欄の上の band に出すメモ。heading はどのメモか (この repo、完了したタスクの途中)。
export type Band = {
  heading: string
  memos: ParkedMemo[]
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
      // notice は送信とメモの操作の結果。
      notice: string
      // parked はこのセッションの repo の open のメモ (hooks/park.tsx が数を prompt の hint 行に出す)。
      parked: ParkedMemo[]
      // band は入力欄の上に出すメモ (hooks/band.tsx)。出さないときは null。
      band: Band | null
    }
  }
}
