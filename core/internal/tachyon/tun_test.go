package tachyon

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func tunEgressFixture() object {
	return object{
		"inbounds": []any{object{"type": "tun", "auto_route": true, "interface_name": "throne-tun"}},
		"route":    object{"auto_detect_interface": true},
		"outbounds": []any{
			object{"type": "vless", "tag": "proxy"},
			object{"type": "direct", "tag": "direct"},
			object{"type": "http", "detour": "proxy"},
			object{"type": "selector", "outbounds": []any{"proxy"}},
			object{"type": "urltest", "outbounds": []any{"proxy"}},
			object{"type": "direct", "bind_interface": "user-interface"},
			object{"type": "direct", "inet4_bind_address": "192.0.2.1"},
		},
		"dns": object{"servers": []any{object{"type": "udp"}, object{"type": "https", "detour": "proxy"}, object{"type": "local"}}},
	}
}

func TestWindowsTUNEgressProtectsSocketsAndPreservesExplicitChoices(t *testing.T) {
	config := tunEgressFixture()
	if err := pinWindowsTUNEgress(config, "physical-interface"); err != nil {
		t.Fatal(err)
	}
	out := config["outbounds"].([]any)
	for _, index := range []int{0, 1} {
		if out[index].(object)["bind_interface"] != "physical-interface" {
			t.Fatal("root socket can loop back into TUN")
		}
	}
	for _, index := range []int{2, 3, 4, 6} {
		if out[index].(object)["bind_interface"] != nil {
			t.Fatal("detour, selector or explicit address changed")
		}
	}
	if out[5].(object)["bind_interface"] != "user-interface" {
		t.Fatal("explicit interface changed")
	}
	dns := config["dns"].(object)["servers"].([]any)
	if dns[0].(object)["bind_interface"] != "physical-interface" || dns[1].(object)["bind_interface"] != nil || dns[2].(object)["bind_interface"] != nil {
		t.Fatal("DNS dialers not protected correctly")
	}
}

func TestWindowsTUNEgressRequiresPhysicalInterface(t *testing.T) {
	for _, name := range []string{"", "throne-tun"} {
		if err := pinWindowsTUNEgress(tunEgressFixture(), name); err == nil {
			t.Fatal("accepted missing or recursive egress")
		}
	}
	config := tunEgressFixture()
	config["route"].(object)["default_interface"] = "configured-physical"
	if err := pinWindowsTUNEgress(config, "detected-physical"); err != nil {
		t.Fatal(err)
	}
	if config["outbounds"].([]any)[0].(object)["bind_interface"] != "configured-physical" {
		t.Fatal("route.default_interface ignored")
	}
}

func TestWindowsTUNEgressLeavesUncapturedConfigsAlone(t *testing.T) {
	for _, mode := range []string{"no-tun", "manual-routes", "explicit-opt-out"} {
		config := tunEgressFixture()
		switch mode {
		case "no-tun":
			delete(config, "inbounds")
		case "manual-routes":
			config["inbounds"].([]any)[0].(object)["auto_route"] = false
		case "explicit-opt-out":
			config["route"].(object)["auto_detect_interface"] = false
		}
		if err := pinWindowsTUNEgress(config, ""); err != nil {
			t.Fatal(mode, err)
		}
		if config["outbounds"].([]any)[0].(object)["bind_interface"] != nil {
			t.Fatal(mode, "was changed")
		}
	}
}

type passiveChild struct{ done chan struct{} }

func (c *passiveChild) Wait() error { <-c.done; return nil }
func (c *passiveChild) Kill() error { close(c.done); return nil }
func (c *passiveChild) StartupError() error {
	return fmt.Errorf("Wintun access denied; continuing in passive mode")
}

type checkedChild struct{}

func (checkedChild) Wait() error { return nil }
func (checkedChild) Kill() error { return nil }

func TestPassiveTUNIsNotReportedReady(t *testing.T) {
	child := &passiveChild{done: make(chan struct{})}
	cleaned := 0
	p := &Plan{Path: "fixture", Bridges: []Bridge{{Address: "127.0.0.1:1"}}}
	_, err := p.Start(context.Background(), func(_ string, args []string) (Child, error) {
		if args[0] == "check" {
			return checkedChild{}, nil
		}
		return child, nil
	}, func(string) (string, func(), error) { return "fixture.json", func() { cleaned++ }, nil })
	if err == nil || !strings.Contains(err.Error(), "Wintun") {
		t.Fatal("passive TUN reported ready", err)
	}
	if cleaned != 2 {
		t.Fatal("config files not cleaned")
	}
	select {
	case <-child.done:
	default:
		t.Fatal("failed native process survived")
	}
}
