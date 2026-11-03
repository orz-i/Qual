package main

import "testing"

func TestLocalOnlyListener(t *testing.T) {
	tests := []struct {
		addr    string
		allowed bool
	}{
		{"127.0.0.1:18080", true},
		{"localhost:18080", true},
		{"[::1]:18080", true},
		{"0.0.0.0:18080", false},
		{":18080", false},
		{"[::]:18080", false},
		{"192.168.1.1:18080", false},
		{"example.com:18080", false},
		{"https://127.0.0.1:18080", false},
	}
	for _, tc := range tests {
		t.Run(tc.addr, func(t *testing.T) {
			if got := loopbackBind(tc.addr); got != tc.allowed {
				t.Errorf("loopbackBind(%q)=%v, want %v", tc.addr, got, tc.allowed)
			}
		})
	}
}
