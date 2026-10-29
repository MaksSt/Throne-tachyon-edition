package tachyon

import (
	"fmt"
	"net"
	"os"
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

type trackedDNSPacket struct {
	net.PacketConn
	closed bool
}

func (p *trackedDNSPacket) Close() error {
	p.closed = true
	return p.PacketConn.Close()
}

func TestNativeDNSPortEscapesExcludedEphemeralRange(t *testing.T) {
	var addresses []string
	var first *trackedDNSPacket
	listenPacket := func(network, address string) (net.PacketConn, error) {
		addresses = append(addresses, address)
		packet, err := net.ListenPacket(network, address)
		if err != nil {
			return nil, err
		}
		if first == nil {
			first = &trackedDNSPacket{PacketConn: packet}
			return first, nil
		}
		return packet, nil
	}
	attempts := 0
	listenTCP := func(network, address string) (net.Listener, error) {
		attempts++
		if attempts == 1 {
			return nil, os.ErrPermission
		}
		return net.Listen(network, address)
	}
	p, sockets, err := reserveNativeDNSPort(listenPacket, listenTCP)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, socket := range sockets {
			_ = socket.Close()
		}
	}()
	if !first.closed {
		t.Fatal("failed UDP reservation leaked")
	}
	if len(addresses) < 2 || addresses[0] != "127.0.0.1:0" || addresses[1] == addresses[0] {
		t.Fatal("retry reused the OS ephemeral allocator inside the excluded range")
	}
	if p < 1024 || len(sockets) != 2 {
		t.Fatal("fallback did not reserve a TCP/UDP port pair")
	}
}
