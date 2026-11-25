package main

import (
	"net"
	"testing"
)

func TestListenLocal(t *testing.T) {
	occupied, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	address := occupied.Addr().String()

	listener, err := listenLocal(address, "")
	if err != nil {
		t.Fatalf("expected a fallback listener: %v", err)
	}
	defer listener.Close()
	if fallback := listener.Addr().(*net.TCPAddr); !fallback.IP.IsLoopback() || fallback.Port == occupied.Addr().(*net.TCPAddr).Port {
		t.Fatalf("unexpected fallback address: %s", fallback)
	}

	for _, override := range []string{address, "0.0.0.0:0"} {
		if listener, err := listenLocal(address, override); err == nil {
			listener.Close()
			t.Errorf("expected override %s to fail", override)
		}
	}
}
