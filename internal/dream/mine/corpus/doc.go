// Package corpus は mine が突き合わせる入力のうち、auto memory のファイルと skill 本体 (SKILL.md と references) を
// 読み込む。読むだけで判定はしない。どの memory を skill へ移すかは mine が決める。
// memory 同士の [[name]] のリンクも拾い、check (リンク切れ) と forget (消す memory のリンク元) が使う。
package corpus
