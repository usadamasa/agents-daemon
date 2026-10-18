import type { Register } from 'claude-code'

import { registerBand, startBand } from './band'
import { registerBoard, startBoard } from './board'
import { registerPark, startPark } from './park'

// hooks.json の modules は 1 つしか書けず、matcher の無い hook は 1 つの plugin で 1 つの event に 1 つしか
// 置けない。mod ごとの hook をここで束ね、session.start はここで持つ。
export const register: Register = (on, options) => {
  on('session.start', async ($, e, next) => {
    await startBoard($)
    await startPark($)
    // band は startPark が読んだメモの一覧を使う。
    await startBand($)

    return next(e)
  })
  registerBoard(on, options)
  registerPark(on, options)
  registerBand(on, options)
}
