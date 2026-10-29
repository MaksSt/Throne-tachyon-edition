package tachyon

import (
	"fmt"
	"strings"
)

// Xray is a profile storage format here, not another running protocol engine.
// Unsupported wire features must fail rather than quietly changing the connection.
func convertXray(wrapper object) (object, error) {
	x, ok := wrapper["outbound"].(object)
	if !ok {
		return nil, fmt.Errorf("tachyon-core: invalid Xray outbound")
	}
	protocol, _ := x["protocol"].(string)
	settings, _ := x["settings"].(object)
	if settings == nil {
		return nil, fmt.Errorf("tachyon-core: Xray %s has no settings", protocol)
	}
	if mux, _ := x["mux"].(object); mux["enabled"] == true {
		return nil, fmt.Errorf("tachyon-core: Xray Mux is a different wire protocol; disable Xray Mux for this profile")
	}
	if err := knownFields(x, "Xray outbound", "protocol", "settings", "streamSettings", "mux", "tag"); err != nil {
		return nil, err
	}
	out := object{"type": protocol, "tag": wrapper["tag"]}
	for _, key := range []string{"detour", "domain_resolver", "domain_strategy"} {
		if v, ok := wrapper[key]; ok {
			out[key] = v
		}
	}
	server := settings
	user := settings
	switch protocol {
	case "vless", "vmess":
		if list, _ := settings["vnext"].([]any); len(list) > 0 {
			if len(list) != 1 {
				return nil, fmt.Errorf("tachyon-core: multiple Xray vnext servers require separate profiles")
			}
			server, _ = list[0].(object)
			users, _ := server["users"].([]any)
			if len(users) != 1 {
				return nil, fmt.Errorf("tachyon-core: expected one Xray user")
			}
			user, _ = users[0].(object)
		}
		out["uuid"] = user["id"]
		if protocol == "vless" {
			if enc, _ := user["encryption"].(string); enc != "" && enc != "none" {
				return nil, fmt.Errorf("tachyon-core: VLESS encryption %s is not supported", strings.Split(enc, ".")[0])
			}
			out["packet_encoding"] = "xudp"
			if flow, _ := user["flow"].(string); flow != "" && flow != "none" {
				out["flow"] = flow
			}
		} else {
			out["security"] = user["security"]
			if out["security"] == nil {
				out["security"] = "auto"
			}
			if v, ok := user["alterId"]; ok {
				out["alter_id"] = v
			}
		}
	case "trojan", "shadowsocks", "socks", "http":
		if list, _ := settings["servers"].([]any); len(list) > 0 {
			if len(list) != 1 {
				return nil, fmt.Errorf("tachyon-core: multiple Xray servers require separate profiles")
			}
			server, _ = list[0].(object)
			user = server
			if users, _ := server["users"].([]any); len(users) > 0 {
				if len(users) != 1 {
					return nil, fmt.Errorf("tachyon-core: expected one proxy user")
				}
				user, _ = users[0].(object)
			}
		}
		if protocol == "trojan" || protocol == "shadowsocks" {
			out["password"] = server["password"]
		}
		if protocol == "shadowsocks" {
			out["method"] = server["method"]
		}
		if protocol == "socks" || protocol == "http" {
			if v, ok := user["user"]; ok {
				out["username"] = v
			}
			if v, ok := user["pass"]; ok {
				out["password"] = v
			}
		}
	case "freedom":
		out["type"] = "direct"
	case "blackhole":
		out["type"] = "block"
	default:
		return nil, fmt.Errorf("tachyon-core: Xray protocol %s has no compatible adapter", protocol)
	}
	if out["type"] != "direct" && out["type"] != "block" {
		if server == nil || user == nil {
			return nil, fmt.Errorf("tachyon-core: malformed Xray server/user")
		}
		out["server"] = server["address"]
		out["server_port"] = server["port"]
	}
	stream, _ := x["streamSettings"].(object)
	if err := convertXrayStream(stream, out); err != nil {
		return nil, err
	}
	return out, nil
}

func knownFields(options object, at string, names ...string) error {
	allowed := map[string]bool{}
	for _, name := range names {
		allowed[name] = true
	}
	for key, value := range options {
		if !allowed[key] && value != nil && value != "" && value != false {
			return fmt.Errorf("tachyon-core: %s.%s has no compatible setting", at, key)
		}
	}
	return nil
}

func renameFields(options object, at string, mapping map[string]string) (object, error) {
	out := object{}
	for key, value := range options {
		if name, ok := mapping[key]; ok {
			out[name] = value
		} else if value != nil && value != "" && value != false {
			return nil, fmt.Errorf("tachyon-core: %s.%s has no compatible setting", at, key)
		}
	}
	return out, nil
}

func convertXrayStream(stream, out object) error {
	if len(stream) == 0 {
		return nil
	}
	if err := knownFields(stream, "streamSettings", "network", "security", "tlsSettings", "realitySettings", "wsSettings", "httpupgradeSettings", "grpcSettings", "xhttpSettings", "tcpSettings", "sockopt"); err != nil {
		return err
	}
	if sockopt, _ := stream["sockopt"].(object); len(sockopt) > 0 {
		mapped, err := renameFields(sockopt, "sockopt", map[string]string{"interface": "bind_interface", "mark": "routing_mark", "tcpFastOpen": "tcp_fast_open", "tcpMptcp": "tcp_multi_path", "tcpKeepAliveInterval": "tcp_keep_alive_interval"})
		if err != nil {
			return err
		}
		for key, value := range mapped {
			out[key] = value
		}
	}
	security, _ := stream["security"].(string)
	if security != "" && security != "none" {
		tls := object{"enabled": true}
		switch security {
		case "tls":
			options, _ := stream["tlsSettings"].(object)
			mapped, err := renameFields(options, "tlsSettings", map[string]string{"serverName": "server_name", "allowInsecure": "insecure", "alpn": "alpn", "fingerprint": "_fingerprint", "cipherSuites": "cipher_suites", "pinnedPeerCertSha256": "certificate_sha256", "useSystemCA": "_system_ca", "minVersion": "min_version", "maxVersion": "max_version"})
			if err != nil {
				return err
			}
			for key, value := range mapped {
				tls[key] = value
			}
			if text, ok := tls["cipher_suites"].(string); ok {
				tls["cipher_suites"] = strings.Split(text, ":")
			}
			if text, ok := tls["certificate_sha256"].(string); ok && text != "" {
				tls["certificate_sha256"] = strings.Split(text, ",")
			}
			delete(tls, "_system_ca")
		case "reality":
			options, _ := stream["realitySettings"].(object)
			if err := knownFields(options, "realitySettings", "serverName", "fingerprint", "publicKey", "password", "shortId", "spiderX", "show"); err != nil {
				return err
			}
			tls["server_name"] = options["serverName"]
			tls["_fingerprint"] = options["fingerprint"]
			key := options["publicKey"]
			if value, ok := options["password"]; ok {
				key = value
			}
			tls["reality"] = object{"enabled": true, "public_key": key, "short_id": options["shortId"]}
		default:
			return fmt.Errorf("tachyon-core: Xray security %s is not supported", security)
		}
		if fingerprint, ok := tls["_fingerprint"].(string); ok && fingerprint != "" {
			tls["utls"] = object{"enabled": true, "fingerprint": fingerprint}
		}
		delete(tls, "_fingerprint")
		out["tls"] = tls
	}
	network, _ := stream["network"].(string)
	transport := object{}
	switch network {
	case "", "raw", "tcp":
		if tcp, _ := stream["tcpSettings"].(object); len(tcp) > 0 {
			return fmt.Errorf("tachyon-core: Xray TCP header settings are not supported")
		}
		return nil
	case "ws", "websocket", "httpupgrade":
		name := "wsSettings"
		if network == "httpupgrade" {
			name = "httpupgradeSettings"
		} else {
			network = "ws"
		}
		options, _ := stream[name].(object)
		mapped, err := renameFields(options, name, map[string]string{"path": "path", "host": "host", "headers": "headers", "maxEarlyData": "max_early_data", "earlyDataHeaderName": "early_data_header_name"})
		if err != nil {
			return err
		}
		transport = mapped
		if network == "ws" {
			if host, ok := transport["host"]; ok {
				headers, _ := transport["headers"].(object)
				if headers == nil {
					headers = object{}
				}
				headers["Host"] = host
				transport["headers"] = headers
				delete(transport, "host")
			}
		}
	case "grpc":
		options, _ := stream["grpcSettings"].(object)
		mapped, err := renameFields(options, "grpcSettings", map[string]string{"serviceName": "service_name", "idle_timeout": "idle_timeout", "health_check_timeout": "ping_timeout", "permit_without_stream": "permit_without_stream"})
		if err != nil {
			return err
		}
		transport = mapped
	case "xhttp", "splithttp":
		network = "xhttp"
		options, _ := stream["xhttpSettings"].(object)
		mapping := map[string]string{"host": "host", "path": "path", "mode": "mode", "headers": "headers", "xPaddingBytes": "x_padding_bytes", "noGRPCHeader": "no_grpc_header", "noSSEHeader": "no_sse_header", "sessionIDPlacement": "session_placement", "sessionIDKey": "session_key", "sessionIDTable": "session_table", "sessionIDLength": "session_length", "seqPlacement": "seq_placement", "seqKey": "seq_key", "uplinkDataPlacement": "uplink_data_placement", "uplinkDataKey": "uplink_data_key", "uplinkChunkSize": "uplink_chunk_size", "uplinkHTTPMethod": "uplink_http_method", "xPaddingObfsMode": "x_padding_obfs_mode", "xPaddingKey": "x_padding_key", "xPaddingHeader": "x_padding_header", "xPaddingPlacement": "x_padding_placement", "xPaddingMethod": "x_padding_method", "scMaxEachPostBytes": "sc_max_each_post_bytes", "scMinPostsIntervalMs": "sc_min_posts_interval_ms", "scMaxBufferedPosts": "sc_max_buffered_posts", "scStreamUpServerSecs": "sc_stream_up_server_secs", "serverMaxHeaderBytes": "server_max_header_bytes", "xmux": "xmux"}
		flat := object{}
		for key, value := range options {
			if key != "extra" {
				flat[key] = value
			}
		}
		if extra, _ := options["extra"].(object); extra != nil {
			for key, value := range extra {
				flat[key] = value
			}
		}
		mapped, err := renameFields(flat, "xhttpSettings", mapping)
		if err != nil {
			return err
		}
		transport = mapped
		if xmux, _ := transport["xmux"].(object); xmux != nil {
			mapped, err := renameFields(xmux, "xhttp.xmux", map[string]string{"maxConcurrency": "max_concurrency", "maxConnections": "max_connections", "cMaxReuseTimes": "c_max_reuse_times", "hMaxRequestTimes": "h_max_request_times", "hMaxReusableSecs": "h_max_reusable_secs", "hKeepAlivePeriod": "h_keep_alive_period"})
			if err != nil {
				return err
			}
			transport["xmux"] = mapped
		}
	default:
		return fmt.Errorf("tachyon-core: Xray transport %s has no compatible adapter", network)
	}
	transport["type"] = network
	out["transport"] = transport
	return nil
}
