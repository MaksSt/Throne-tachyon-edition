package process

import "testing"

func TestEngineOutputDetectsPassiveTUNAcrossWrites(t *testing.T) {
	output := &engineOutput{}
	child := &engineProcess{output: output}
	_, _ = output.Write([]byte("WARN inbound tun[tun-in]: failed to open Wintun interface: Access is denied; continu"))
	_, _ = output.Write([]byte("ing in passive mode\n"))
	if child.StartupError() == nil {
		t.Fatal("passive TUN accepted as healthy")
	}
	_, _ = output.Write(make([]byte, 9000))
	if child.StartupError() == nil {
		t.Fatal("startup failure lost after log tail rotated")
	}
}
