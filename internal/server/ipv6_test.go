package server

import (
	"testing"
)

func TestDeriveIPv6FromV4(t *testing.T) {
	tests := []struct {
		name     string
		ipv4     string
		subnet   string
		wantIPv6 string
	}{
		{
			name:     "basic conversion",
			ipv4:     "10.88.1.5",
			subnet:   "10.88.1.0/24",
			wantIPv6: "fd00:a:58:1::5",
		},
		{
			name:     "larger host part",
			ipv4:     "10.88.1.100",
			subnet:   "10.88.1.0/24",
			wantIPv6: "fd00:a:58:1::100",
		},
		{
			name:     "different subnet",
			ipv4:     "10.192.0.15",
			subnet:   "10.192.0.0/24",
			wantIPv6: "fd00:a:c0:0::15",
		},
		{
			name:     "small numbers",
			ipv4:     "10.0.0.1",
			subnet:   "10.0.0.0/24",
			wantIPv6: "fd00:a:0:0::1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := deriveIPv6FromV4(tt.ipv4, tt.subnet)
			if got != tt.wantIPv6 {
				t.Errorf("deriveIPv6FromV4(%q, %q) = %q, want %q", tt.ipv4, tt.subnet, got, tt.wantIPv6)
			}
		})
	}
}

func TestExtractIPFromEndpoint(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		wantIP   string
	}{
		{
			name:     "IPv4 with port",
			endpoint: "192.168.1.100:51820",
			wantIP:   "192.168.1.100",
		},
		{
			name:     "IPv6 with port",
			endpoint: "[fd00:a:58:1::5]:51820",
			wantIP:   "fd00:a:58:1::5",
		},
		{
			name:     "IPv6 global with port",
			endpoint: "[2400:ca00::1]:51820",
			wantIP:   "2400:ca00::1",
		},
		{
			name:     "empty endpoint",
			endpoint: "",
			wantIP:   "",
		},
		{
			name:     "just IP no port",
			endpoint: "192.168.1.100",
			wantIP:   "192.168.1.100",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractIPFromEndpoint(tt.endpoint)
			if got != tt.wantIP {
				t.Errorf("extractIPFromEndpoint(%q) = %q, want %q", tt.endpoint, got, tt.wantIP)
			}
		})
	}
}