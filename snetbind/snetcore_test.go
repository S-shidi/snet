package snetbind

import (
	"testing"

	"snet/internal/constants"
)

func TestEnforceFixedServer(t *testing.T) {
	cases := []struct {
		server string
		wantErr bool
	}{
		{"", false},
		{constants.DefaultServerAddr, false},
		{constants.DefaultServerAddr + "/", false}, // trailing slash normalizes
		{"https://snet.uizhi.eu.org:8090", false},
		{"http://127.0.0.1:8080", true},
		{"https://evil.example", true},
	}
	for _, tc := range cases {
		if err := enforceFixedServer(tc.server); (err != nil) != tc.wantErr {
			t.Fatalf("enforceFixedServer(%q) err=%v, wantErr=%v", tc.server, err, tc.wantErr)
		}
	}
}