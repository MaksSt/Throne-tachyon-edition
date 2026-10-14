package tachyon

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strconv"

	"golang.org/x/net/proxy"
)

type probeAccess struct {
	address string
	secret  string
	users   map[string]string
}

// One authenticated loopback SOCKS listener routes each probe to its exact
// outbound. This measures the real Tachyon data plane, including live profiles.
func prepareProbes(raw []byte) ([]byte, *probeAccess, error) {
	var config map[string]json.RawMessage
	if err := json.Unmarshal(raw, &config); err != nil || config == nil {
		return nil, nil, fmt.Errorf("Tachyon probe: configuration must be an object")
	}
	access := &probeAccess{users: make(map[string]string)}
	address, secret, err := controllerCredentials()
	if err != nil {
		return nil, nil, err
	}
	access.address, access.secret = address, secret
	inboundTag := "throne-probe-" + secret
	var users []map[string]string
	var rules []json.RawMessage
	for _, section := range []string{"outbounds", "endpoints"} {
		var entries []struct {
			Tag string `json:"tag"`
		}
		if value, ok := config[section]; ok {
			if err := json.Unmarshal(value, &entries); err != nil {
				return nil, nil, err
			}
		}
		for _, entry := range entries {
			if entry.Tag == "" {
				continue
			}
			if _, exists := access.users[entry.Tag]; exists {
				continue
			}
			user := strconv.Itoa(len(users))
			access.users[entry.Tag] = user
			users = append(users, map[string]string{"username": user, "password": secret})
			rule, _ := json.Marshal(map[string]any{"inbound": inboundTag, "auth_user": user, "action": "route", "outbound": entry.Tag})
			rules = append(rules, rule)
		}
	}
	if len(users) == 0 {
		return raw, access, nil
	}
	_, portText, _ := net.SplitHostPort(address)
	port, _ := strconv.Atoi(portText)
	var inbounds []json.RawMessage
	if value, ok := config["inbounds"]; ok {
		if err := json.Unmarshal(value, &inbounds); err != nil {
			return nil, nil, err
		}
	}
	inbound, _ := json.Marshal(map[string]any{"type": "socks", "tag": inboundTag, "listen": "127.0.0.1", "listen_port": port, "users": users})
	inbounds = append(inbounds, inbound)
	config["inbounds"], err = json.Marshal(inbounds)
	if err != nil {
		return nil, nil, err
	}
	var route map[string]json.RawMessage
	if value, ok := config["route"]; ok {
		if err := json.Unmarshal(value, &route); err != nil {
			return nil, nil, err
		}
	}
	if route == nil {
		route = make(map[string]json.RawMessage)
	}
	var original []json.RawMessage
	if value, ok := route["rules"]; ok {
		if err := json.Unmarshal(value, &original); err != nil {
			return nil, nil, err
		}
	}
	route["rules"], err = json.Marshal(append(rules, original...))
	if err != nil {
		return nil, nil, err
	}
	config["route"], err = json.Marshal(route)
	if err != nil {
		return nil, nil, err
	}
	prepared, err := json.Marshal(config)
	return prepared, access, err
}

func (r *Runtime) Dialer(tag string) (func(context.Context, string, string) (net.Conn, error), error) {
	user, exists := r.probes.users[tag]
	if !exists {
		return nil, fmt.Errorf("Tachyon: no outbound with tag %s found", tag)
	}
	dialer, err := proxy.SOCKS5("tcp", r.probes.address, &proxy.Auth{User: user, Password: r.probes.secret}, &net.Dialer{})
	if err != nil {
		return nil, err
	}
	return dialer.(proxy.ContextDialer).DialContext, nil
}
