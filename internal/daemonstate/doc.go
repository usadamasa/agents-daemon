// Package daemonstate は動いている daemon を 1 つに決め、外へ公開するもの (起動の排他の daemon.lock、status.json、
// PID ファイル) の書式と読み書き、それを使って daemon が動いているかを確かめ、止める手続きを持つ。
//
// 書くのは daemon の監視ループ (internal/daemon)、読むのは status / doctor / deps / adopt のような daemon の外の
// 呼び手。読み手が監視ループを import せずに済むよう、ループとは別のパッケージに置く。
package daemonstate
