package tachyon

import (
	"fmt"
	"log"
)

// Windows installs more-specific /1 routes through Wintun. Without an explicit
// socket interface the proxy's own connection follows those routes back into
// itself, even when route.auto_detect_interface is set in the input config.
func pinWindowsTUNEgress(config object, physicalInterface string) error {
	inbounds, _ := config["inbounds"].([]any)
	tunNames := map[string]bool{}
	enabled := false
	for _, item := range inbounds {
		inbound, _ := item.(object)
		if inbound["type"] == "tun" && inbound["auto_route"] == true {
			enabled = true
			name, _ := inbound["interface_name"].(string)
			if name != "" {
				tunNames[name] = true
			}
		}
	}
	if !enabled {
		return nil
	}
	route, _ := config["route"].(object)
	if route["auto_detect_interface"] == false {
		return nil
	}
	if configured, _ := route["default_interface"].(string); configured != "" {
		physicalInterface = configured
	}
	if physicalInterface == "" || tunNames[physicalInterface] {
		return fmt.Errorf("tachyon-core: a physical default interface is required for Windows TUN; refusing a routing loop")
	}
	pin := func(dialer object) {
		for _, key := range []string{"detour", "bind_interface", "inet4_bind_address", "inet6_bind_address"} {
			if value, _ := dialer[key].(string); value != "" {
				return
			}
		}
		dialer["bind_interface"] = physicalInterface
	}
	for _, section := range []string{"outbounds", "endpoints"} {
		items, _ := config[section].([]any)
		for _, item := range items {
			dialer, _ := item.(object)
			switch dialer["type"] {
			case "selector", "urltest", "auto-selector", "chain", "block", "dns":
				continue
			}
			pin(dialer)
		}
	}
	if dns, _ := config["dns"].(object); dns != nil {
		servers, _ := dns["servers"].([]any)
		for _, item := range servers {
			server, _ := item.(object)
			switch server["type"] {
			case "udp", "tcp", "tls", "https", "http3", "quic", "h3":
				pin(server)
			}
		}
	}
	log.Printf("Tachyon TUN: physical egress %q", physicalInterface)
	return nil
}
