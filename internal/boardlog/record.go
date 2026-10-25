package boardlog

import (
	"errors"
	"time"
)

// Record は出来事 event をログに 1 行書く。kv は key=value の並びで、先頭に session を添える (空なら
// 添えない)。
func Record(stateDir, session, event string, kv []string, now time.Time) error {
	if !validEvent(event) {
		return errors.New("出来事の名前は小文字、数字、. と - だけにする")
	}
	fields, err := parseFields(kv)
	if err != nil {
		return err
	}
	if session != "" {
		fields = append([]field{f("session", session)}, fields...)
	}
	write(stateDir, func() time.Time { return now }, event, fields...)
	return nil
}
