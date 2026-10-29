package tachyon

import (
	"fmt"
	"io"
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
	var last error
	for attempt := 0; attempt < 32; attempt++ {
		packet, err := net.ListenPacket("udp4", "127.0.0.1:0")
		if err != nil {
			last = err
			continue
		}
		p := packet.LocalAddr().(*net.UDPAddr).Port
		tcp, err := net.Listen("tcp4", packet.LocalAddr().String())
		if err == nil {
			return p, []io.Closer{tcp, packet}, nil
		}
		_ = packet.Close()
		last = err
	}
	return 0, nil, fmt.Errorf("reserve native DNS TCP/UDP port: %w", last)
}
