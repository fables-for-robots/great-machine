package web

import "testing"

func TestIsLoopback(t *testing.T) {
	tests := []struct {
		addr string
		want bool
	}{
		{"127.0.0.1:8080", true},
		{"127.0.0.1:0", true},
		{"127.9.9.9:8080", true},
		{"localhost:8080", true},
		{"LOCALHOST:8080", true},
		{"[::1]:8080", true},
		{":8080", false}, // every address
		{"0.0.0.0:8080", false},
		{"[::]:8080", false},
		{"192.168.1.5:8080", false},
		{"example.com:80", false},
		{"localhost.example.com:80", false},
		{"127.0.0.1", false}, // no port: not as --web-addr has it
		{"localhost", false},
		{"::1", false},
		{"", false},
		{"garbage", false},
		{"a:b:c", false},
		{"[::1%lo0]:8080", false},
	}
	for _, tt := range tests {
		if got := IsLoopback(tt.addr); got != tt.want {
			t.Errorf("IsLoopback(%q) = %v, want %v", tt.addr, got, tt.want)
		}
	}
}
