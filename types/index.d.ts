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
  task_id?: string
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

// ParkedMemo はメモ 1 つを band のメモの行と hint 行に出す分だけにしたもの。at は最後に置かれた時刻。
export type ParkedMemo = {
  id: string
  kind: ParkingCard['kind']
  title: string
  at: string
}

export type BoardRow = {
  repo: string
  title: string
  cards: BoardCard[]
  parking: ParkingCard[]
}

// GraphNode は依存の DAG のノード 1 つ (タスク 1 つ、タスクの無いセッション、端点の参照だけの置き場)。
export type GraphNode = {
  key: string
  kind: 'task' | 'todo' | 'session' | 'ref'
  state: 'working' | 'done' | 'unknown' | 'question' | 'permission' | 'error' | 'limit' | 'pending' | 'completed' | 'exited'
  title: string
  detail?: string
  ref?: string
  cwd?: string
  session_id?: string
  session?: string
  live: boolean
  depth: number
  downstream: number
  parked: number
  blocked_by: string[]
  deadlock: boolean
}

export type EdgeEndpoint = {
  ref: string
  cwd: string
  repo: string
}

// GraphLink は辺 1 本。kind task は同じセッションの blockedBy、kind edge は `edge add` で書かれた辺で edge に元の記録を持つ。
export type GraphLink = {
  before: string
  after: string
  kind: 'task' | 'edge'
  edge?: { id: string; before: EdgeEndpoint; after: EdgeEndpoint; note?: string }
}

// GraphRow はレーンの行 1 つ。lanes は行頭の罫線で、ノードの行は `*` がそのノードのレーンの位置。node が無ければ
// 辺をつなぐだけの行。
export type GraphRow = {
  lanes: string
  node?: string
}

export type BoardGraph = {
  nodes: GraphNode[]
  links: GraphLink[]
  longest_chain: string[]
  // rows は完了したノードを落として組んだレーンの行、rows_with_completed は全部を含めて組んだ行。
  rows: GraphRow[]
  rows_with_completed: GraphRow[]
}

export type BoardData = {
  domain: 'home' | 'work'
  daemon_running: boolean
  updated_at?: string
  columns: BoardColumn[]
  // here は開いたセッションの行の鍵 (BoardRow.repo と同じ規則)。この行の parking が hint 行と band のメモになる。
  here: string
  rows: BoardRow[]
  graph: BoardGraph
}

declare module 'claude-code' {
  interface PluginState {
    'agents-daemon': {
      // board は最後に読めたボード。読めていなければ null。
      board: BoardData | null
      // error は直近の読み込みの失敗理由。読めたら空にする。
      error: string
      // target は選んだノードの key かメモの key (park:<id>)。B の pane と A の band が共有する。未選択なら空。
      target: string
      // unfolded は B の木で畳まずに開くノードの key。
      unfolded: string[]
      // picking は B の詳細で Add blocker の候補を出しているか。
      picking: boolean
      // notice は送信とメモの操作の結果。
      notice: string
      // parked はこのセッションの repo の open のメモ。board の here の行から hooks/board-data.ts が置き、
      // hooks/park.tsx が数を prompt の hint 行に、hooks/band.tsx がメモの行に出す。
      parked: ParkedMemo[]
      // front は band のメモの行の先頭に寄せるメモの id (completed になったタスクの途中で置いたもの、hooks/band.tsx)。
      // 並びの指示だけで、中身は parked から取る。
      front: string[]
      // showCompleted は band で完了したノードを出すか。既定は畳む。
      showCompleted: boolean
      // logged はボードの経路のログに slot ごとに最後に書いた中身 (hooks/log.ts)。同じものを書き直さないため。
      logged: Record<string, string>
    }
  }
}
