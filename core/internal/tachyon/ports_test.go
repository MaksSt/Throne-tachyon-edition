package tachyon

import (
	"fmt"
	"net"
	"testing"
)

func TestNativeDNSPortReservesAndReleasesBothProtocols(t *testing.T) {
	p, sockets, err := reserveNativePort(true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, socket := range sockets {
			_ = socket.Close()
		}
	}()
	address := net.JoinHostPort("127.0.0.1", fmt.Sprint(p))
	if listener, err := net.Listen("tcp4", address); err == nil {
		listener.Close()
		t.Fatal("TCP port was not reserved")
	}
	if listener, err := net.ListenPacket("udp4", address); err == nil {
		listener.Close()
		t.Fatal("UDP port was not reserved")
	}
	for _, socket := range sockets {
		_ = socket.Close()
	}
	tcp, err := net.Listen("tcp4", address)
	if err != nil {
		t.Fatal("TCP reservation leaked", err)
	}
	defer tcp.Close()
	udp, err := net.ListenPacket("udp4", address)
	if err != nil {
		t.Fatal("UDP reservation leaked", err)
	}
	defer udp.Close()
}
