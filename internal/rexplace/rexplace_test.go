package rexplace

import "testing"

func TestFromEnv(t *testing.T) {
	tests := []struct {
		name   string
		env    map[string]string
		want   Place
		wantOK bool
	}{
		{name: "block があれば rex の中", env: map[string]string{BlockEnv: "block:1"}, want: Place{Block: "block:1"}, wantOK: true},
		{name: "空の値は無いものとして扱う", env: map[string]string{BlockEnv: ""}, wantOK: false},
		{name: "無ければ rex の外", env: map[string]string{}, wantOK: false},
		{name: "rex がもう入れない REX_SESSION だけでは rex の外", env: map[string]string{"REX_SESSION": "session:1"}, wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := FromEnv(func(k string) string { return tt.env[k] })
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("FromEnv = %+v, %v; want %+v, %v", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}
