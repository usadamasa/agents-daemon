// Package domain はセッションの cwd から、私用と業務のどちらの作業かを判定する (#121)。
//
// 業務のものはインターネットに出さず、私用と同じボードにも混ぜない。業務の端末では私用の
// リポジトリも触るので、1 つの daemon の下に 2 つの domain が混ざる。判定は許可リスト方式で、
// 私用と言えるのは config の privatePaths の下だけにする。それ以外と判定できないものは
// defaultDomain (既定は業務) に倒す。判定を誤っても、業務のものが私用の側へ漏れない向きにするため。
// 私用の端末では defaultDomain を private にすれば、パスに関係なくすべて私用になる。
package domain

import (
	"path/filepath"
	"strings"
)

// Domain は作業の区分。タスクやパーキングの置き場 (<domain>/) の名前にもなる。
type Domain string

const (
	Private Domain = "private"
	Work    Domain = "work"
)

// Parse は config の defaultDomain の値を Domain にする。private 以外 (空、typo を含む) は業務に倒す。
func Parse(s string) Domain {
	if Domain(s) == Private {
		return Private
	}
	return Work
}

// Classifier は cwd から domain を決める。ゼロ値はすべてを業務と判定する。
type Classifier struct {
	privatePaths []string
	fallback     Domain
}

// New は config の privatePaths と defaultDomain から Classifier を作る。
// privatePaths の先頭の ~ は home に展開する。絶対パスにならないもの (相対パス、空) は捨てる。
// 相対パスはどこを起点にするかで意味が変わり、私用の範囲が意図せず広がりうるため。
func New(home string, privatePaths []string, defaultDomain string) Classifier {
	c := Classifier{fallback: Parse(defaultDomain)}
	for _, p := range privatePaths {
		if p == "~" || strings.HasPrefix(p, "~/") {
			p = filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
		if !filepath.IsAbs(p) {
			continue
		}
		c.privatePaths = append(c.privatePaths, filepath.Clean(p))
	}
	return c
}

// Fallback は privatePaths に当たらない cwd の domain。
func (c Classifier) Fallback() Domain {
	if c.fallback == "" {
		return Work
	}
	return c.fallback
}

// PrivatePaths は展開済みの privatePaths。doctor が表示に使う。
func (c Classifier) PrivatePaths() []string {
	return append([]string(nil), c.privatePaths...)
}

// Classify は cwd の domain を返す。cwd が privatePaths のどれかと同じか、その下なら私用、
// それ以外 (空や相対パスを含む) は Fallback。前方一致はパスの区切りで見る
// (/src/usadamasa は /src/usadamasa-work に当たらない)。
func (c Classifier) Classify(cwd string) Domain {
	if filepath.IsAbs(cwd) {
		cwd = filepath.Clean(cwd)
		for _, p := range c.privatePaths {
			if within(cwd, p) {
				return Private
			}
		}
	}
	return c.Fallback()
}

// within は path が dir と同じか、その下にあるか。
func within(path, dir string) bool {
	if path == dir {
		return true
	}
	if dir == string(filepath.Separator) {
		return true
	}
	return strings.HasPrefix(path, dir+string(filepath.Separator))
}
