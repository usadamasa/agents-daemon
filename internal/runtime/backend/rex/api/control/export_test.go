package control

import "errors"

// ErrorCode は err が server の返した失敗なら、その code と status を返す。
func ErrorCode(err error) (code string, status int) {
	var e *apiError
	if errors.As(err, &e) {
		return e.code, e.status
	}
	return "", 0
}

// WelcomeRevision は welcome が言った api_revision を返す。
func (c *Conn) WelcomeRevision() int { return c.welcome.apiRevision }
