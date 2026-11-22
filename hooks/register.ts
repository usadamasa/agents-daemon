import type { Register } from 'claude-code'

import { registerHandoffNotice } from './handoff-notice'
import { registerNotice } from './notice'
import { openOnStart, registerOpenApp, startOpenApp } from './open-app'
import { registerPark, startPark } from './park'
import { countTabLabel, startTabLabel } from './tab-label'
import { registerTaskTools } from './task-tools'
import { markWorkInfer, startWorkInfer } from './work-infer'

// hooks.json の modules は 1 つしか書けず、matcher の無い hook は 1 つの plugin で 1 つの event に 1 つしか
// 置けない。mod ごとの hook をここで束ね、session.start と turn.complete はここで持つ。依存図の描画と操作はアプリ (gui/) が持ち、
// mod にはセッションの中でしかできないこと (model が呼ぶ tool の登録、tool の結果に添える通知、$.model.fork、
// tool.describe、slash command) が残る。
// cc-premise: 2.1.302 hooks modules は 1 つ、matcher の無い hook は event ごとに 1 つ
export const register: Register = (on, options) => {
  on('session.start', async ($, e, next) => {
    await startPark($)
    await startOpenApp($)
    await openOnStart($, e.isInteractive)
    startWorkInfer($)
    startTabLabel($)

    return next(e)
  })
  // main のターンが answer で終わったことだけを印にし、推定は各 mod の timer が拾う (待つと応答の表示が止まる)。
  on('turn.complete', async ($, e, next) => {
    if (e.agentId === undefined && e.reason === 'answer') {
      await markWorkInfer($)
      await countTabLabel($)
    }
    return next(e)
  })
  registerPark(on, options)
  registerOpenApp(on, options)
  registerNotice(on, options)
  registerHandoffNotice(on, options)
  registerTaskTools(on, options)
}
