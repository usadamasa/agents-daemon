// hooks/deps.tsx、hooks/notice.ts などの mod が $.state に置く値の型。形は `agents-daemon deps` の JSON (internal/deps) に合わせる。

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

// DepsSession は repo で生きているセッション 1 つ (メモを渡す宛先)。subject はそのセッションが何をしているか。
export type DepsSession = {
  session_id: string
  session: string
  subject: string
  live: boolean
}

// DepsRepo は repo 1 つ。sessions は表示名の順、memos は open のメモを置いた順。
export type DepsRepo = {
  repo: string
  title: string
  sessions: DepsSession[]
  memos: ParkingCard[]
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
  // session は表示名 (同名のセッションがあれば session ID の先頭が付く)、session_name はセッション名そのもの
  // (SendMessage の宛先に書く形)。
  session?: string
  session_name?: string
  live: boolean
  depth: number
  downstream: number
  parked: number
  // memos はこのタスクの description に id が書かれたメモ (書かれた順)。status を問わない。
  memos: MemoRef[]
  blocked_by: string[]
  deadlock: boolean
}

// MemoRef はタスクが扱うメモ 1 つ。
export type MemoRef = {
  id: string
  title: string
  status: 'open' | 'handed' | 'discarded'
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
// 辺をつなぐだけの行。marks は longest chain の辺を描いた lanes の桁 (昇順)。
export type GraphRow = {
  lanes: string
  node?: string
  marks?: number[]
}

export type DepsGraph = {
  nodes: GraphNode[]
  links: GraphLink[]
  longest_chain: string[]
  // rows は完了したノードを落として組んだレーンの行、rows_with_completed は全部を含めて組んだ行。
  rows: GraphRow[]
  rows_with_completed: GraphRow[]
}

export type DepsData = {
  daemon_running: boolean
  updated_at?: string
  // here は開いたセッションの repo の鍵 (DepsRepo.repo と同じ規則)。
  here: string
  repos: DepsRepo[]
  graph: DepsGraph
}

declare module 'claude-code' {
  interface PluginState {
    'agents-daemon': {
      // deps は最後に読めた依存図。読めていなければ null。
      deps: DepsData | null
      // error は直近の読み込みの失敗理由。読めたら空にする。
      error: string
      // target は依存図の pane で選んだノードの key かメモの key (park:<id>)。未選択なら空。
      target: string
      // picking はノードの詳細で Add blocker の候補を出しているか。
      picking: boolean
      // spawning はメモの詳細で「新しいセッションを起こして渡す」の repo の候補を出しているか。出しているなら頼むこと
      // (task / issue)、出していなければ空。
      spawning: '' | 'task' | 'issue'
      // notice は送信とメモの操作の結果。
      notice: string
      // told はファイルを触った tool の結果で model に伝えたメモの id (hooks/notice.ts)。同じメモを 2 度伝えない。
      // main の会話が compact されたら空にする。
      told: string[]
      // showCompleted は依存図の pane で完了したノードを出すか (graph.rows_with_completed を描く)。既定は畳む。
      showCompleted: boolean
      // logged は出来事のログに slot ごとに最後に書いた中身 (hooks/log.ts)。同じものを書き直さないため。
      logged: Record<string, string>
    }
  }
}
