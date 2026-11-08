package detect

import "time"

// transcriptUnix は fuzz が resetsAt の epoch 秒を時刻にするのに使う。
func transcriptUnix(sec int64) time.Time {
	return time.Unix(sec, 0)
}
