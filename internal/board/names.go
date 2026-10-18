package board

// sessionNames はカードとメモの source に出すセッションの短い名前を決める。セッション名 (sessions) を使い、
// 同じ名前のセッションが複数あれば session ID の先頭を足して見分ける。名前が無ければ session ID の先頭にする。
// ids は名前を付けるセッションの session ID。
func sessionNames(ids map[string]bool, sessions map[string]string) map[string]string {
	count := map[string]int{}
	for id := range ids {
		count[sessions[id]]++
	}
	names := map[string]string{}
	for id := range ids {
		switch name := sessions[id]; {
		case name == "":
			names[id] = shortID(id)
		case count[name] > 1:
			names[id] = name + " " + shortID(id)
		default:
			names[id] = name
		}
	}
	return names
}

// shortID は session ID の先頭 8 文字 (UUID の最初の区切りまで)。
func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
