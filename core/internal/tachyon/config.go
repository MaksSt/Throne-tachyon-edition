// Package tachyon adapts the pinned sing-box-compatible Tachyon CLI and Clash API.
package tachyon

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
)

// Prepare keeps the profile's routing/DNS/outbounds and replaces only Throne's
// API plumbing. The control listener is private, authenticated and loopback-only.
func Prepare(raw []byte, controller, secret string) ([]byte, error) {
	var config map[string]json.RawMessage
	if err := json.Unmarshal(raw, &config); err != nil {
		return nil, err
	}
	if config == nil {
		return nil, errors.New("Tachyon: configuration must be a JSON object")
	}
	if err := normalizeResolverReferences(config); err != nil {
		return nil, err
	}
	if err := normalizeDNSRecords(config); err != nil {
		return nil, err
	}
	for _, section := range []string{"inbounds", "outbounds", "endpoints"} {
		var entries []struct {
			Type string `json:"type"`
		}
		if value, ok := config[section]; ok {
			if err := json.Unmarshal(value, &entries); err != nil {
				return nil, fmt.Errorf("%s: %w", section, err)
			}
		}
		for _, entry := range entries {
			switch entry.Type {
			case "tun", "tproxy", "redirect", "auto-selector", "auto_selector", "autoselector", "openvpn", "openconnect":
				return nil, fmt.Errorf("Tachyon backend does not support %s; select built-in sing-box", entry.Type)
			}
		}
	}
	var services []map[string]json.RawMessage
	if value, ok := config["services"]; ok {
		if err := json.Unmarshal(value, &services); err != nil {
			return nil, err
		}
	}
	var kept []map[string]json.RawMessage
	for _, service := range services {
		var kind string
		if err := json.Unmarshal(service["type"], &kind); err != nil {
			return nil, err
		}
		if kind != "api" {
			kept = append(kept, service)
		}
	}
	delete(config, "services")
	if len(kept) > 0 {
		value, err := json.Marshal(kept)
		if err != nil {
			return nil, err
		}
		config["services"] = value
	}
	var experimental map[string]json.RawMessage
	if value, ok := config["experimental"]; ok {
		if err := json.Unmarshal(value, &experimental); err != nil {
			return nil, err
		}
	}
	if experimental == nil {
		experimental = make(map[string]json.RawMessage)
	}
	api, err := json.Marshal(map[string]any{"external_controller": controller, "secret": secret, "access_control_allow_origin": []string{}})
	if err != nil {
		return nil, err
	}
	experimental["clash_api"] = api
	value, err := json.Marshal(experimental)
	if err != nil {
		return nil, err
	}
	config["experimental"] = value
	return json.Marshal(config)
}

func controllerCredentials() (string, string, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return "", "", err
	}
	address := listener.Addr().String()
	if err = listener.Close(); err != nil {
		return "", "", err
	}
	key := make([]byte, 32)
	if _, err = rand.Read(key); err != nil {
		return "", "", err
	}
	return address, hex.EncodeToString(key), nil
}

// v0.1.1 validates and reads resolver tags as strings. Throne emits the
// equivalent {server, strategy: ""} object even for its default DNS policy.
// Non-default query options cannot be discarded without changing DNS behavior.
func normalizeResolver(raw json.RawMessage, path string) (json.RawMessage, error) {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.TrimSpace(raw)[0] != '{' {
		return raw, nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, err
	}
	var server string
	if err := json.Unmarshal(object["server"], &server); err != nil || server == "" {
		return nil, fmt.Errorf("%s: Tachyon requires a DNS resolver tag", path)
	}
	for key, value := range object {
		if key == "server" {
			continue
		}
		if key == "strategy" {
			var strategy string
			if json.Unmarshal(value, &strategy) == nil && (strategy == "" || strategy == "as_is") {
				continue
			}
		}
		return nil, fmt.Errorf("%s: Tachyon v0.1.1 does not support resolver query options; select built-in sing-box", path)
	}
	return json.Marshal(server)
}

func normalizeResolverReferences(config map[string]json.RawMessage) error {
	if value, exists := config["route"]; exists {
		var route map[string]json.RawMessage
		if err := json.Unmarshal(value, &route); err != nil {
			return err
		}
		if resolver, exists := route["default_domain_resolver"]; exists {
			normalized, err := normalizeResolver(resolver, "route.default_domain_resolver")
			if err != nil {
				return err
			}
			route["default_domain_resolver"] = normalized
			config["route"], err = json.Marshal(route)
			if err != nil {
				return err
			}
		}
	}
	for _, section := range []string{"outbounds", "endpoints", "dns.servers"} {
		owner := config
		key := section
		if section == "dns.servers" {
			var dns map[string]json.RawMessage
			if raw, exists := config["dns"]; exists {
				if err := json.Unmarshal(raw, &dns); err != nil {
					return err
				}
			}
			if dns == nil {
				continue
			}
			owner, key = dns, "servers"
		}
		value, exists := owner[key]
		if !exists {
			continue
		}
		var entries []map[string]json.RawMessage
		if err := json.Unmarshal(value, &entries); err != nil {
			return err
		}
		changed := false
		for index, entry := range entries {
			if resolver, exists := entry["domain_resolver"]; exists {
				normalized, err := normalizeResolver(resolver, fmt.Sprintf("%s[%d].domain_resolver", section, index))
				if err != nil {
					return err
				}
				entry["domain_resolver"] = normalized
				changed = true
			}
		}
		if changed {
			var err error
			owner[key], err = json.Marshal(entries)
			if err != nil {
				return err
			}
			if section == "dns.servers" {
				config["dns"], err = json.Marshal(owner)
				if err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// Live tests follow Throne's convention: prefer its proxy tag, otherwise the
// custom full config's explicit final or first outbound.
func ProbeTag(raw []byte) string {
	var config struct {
		Route struct {
			Final string `json:"final"`
		} `json:"route"`
		Outbounds []struct {
			Tag string `json:"tag"`
		} `json:"outbounds"`
	}
	if json.Unmarshal(raw, &config) != nil {
		return ""
	}
	for _, outbound := range config.Outbounds {
		if outbound.Tag == "proxy" {
			return "proxy"
		}
	}
	if config.Route.Final != "" {
		return config.Route.Final
	}
	if len(config.Outbounds) > 0 {
		return config.Outbounds[0].Tag
	}
	return ""
}

// An explicit use_default_outbound request follows route.final, even when a
// separate outbound named proxy exists in a custom configuration.
func DefaultTag(raw []byte) string {
	var config struct {
		Route struct {
			Final string `json:"final"`
		} `json:"route"`
		Outbounds []struct {
			Tag string `json:"tag"`
		} `json:"outbounds"`
	}
	if json.Unmarshal(raw, &config) != nil {
		return ""
	}
	if config.Route.Final != "" {
		return config.Route.Final
	}
	if len(config.Outbounds) > 0 {
		return config.Outbounds[0].Tag
	}
	return ""
}
