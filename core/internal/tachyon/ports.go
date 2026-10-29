package tachyon

import (
	"fmt"
	"io"
	"math/rand/v2"
	"net"
)

// Native DNS listens on both protocols. A free TCP port may be occupied or
// excluded for UDP on Windows, so reserve both before publishing its number.
func reserveNativePort(udp bool) (int, []io.Closer, error) {
	if !udp {
		tcp, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			return 0, nil, err
		}
		return tcp.Addr().(*net.TCPAddr).Port, []io.Closer{tcp}, nil
	}
	return reserveNativeDNSPort(net.ListenPacket, net.Listen)
}

func reserveNativeDNSPort(listenPacket func(string, string) (net.PacketConn, error), listenTCP func(string, string) (net.Listener, error)) (int, []io.Closer, error) {
	var last error
	for attempt := 0; attempt < 32; attempt++ {
		address := "127.0.0.1:0"
		if attempt > 0 {
			// Windows can allocate sequential UDP ports inside a large TCP
			// exclusion range. Sample the full unprivileged range instead of
			// repeatedly asking its UDP allocator for another excluded port.
			address = net.JoinHostPort("127.0.0.1", fmt.Sprint(1024+rand.IntN(65536-1024)))
		}
		packet, err := listenPacket("udp4", address)
		if err != nil {
			last = err
			continue
		}
		p := packet.LocalAddr().(*net.UDPAddr).Port
		tcp, err := listenTCP("tcp4", packet.LocalAddr().String())
		if err == nil {
			return p, []io.Closer{tcp, packet}, nil
		}
		_ = packet.Close()
		last = err
	}
	return 0, nil, fmt.Errorf("reserve native DNS TCP/UDP port: %w", last)
}
