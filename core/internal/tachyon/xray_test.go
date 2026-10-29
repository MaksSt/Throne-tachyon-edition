package tachyon

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func xrayFixture(stream object) object {
	return object{"type": "_throne_xray", "tag": "proxy", "outbound": object{"protocol": "vless",
		"settings":       object{"address": "example.com", "port": 443, "id": "00000000-0000-0000-0000-000000000001", "encryption": "none", "flow": "xtls-rprx-vision"},
		"streamSettings": stream}}
}

func TestXrayRealityConversion(t *testing.T) {
	input := xrayFixture(object{"network": "raw", "security": "reality", "realitySettings": object{"serverName": "example.com", "fingerprint": "chrome", "password": "public-key", "shortId": "1234"}})
	input["detour"] = "entry"
	out, err := convertXray(input)
	if err != nil {
		t.Fatal(err)
	}
	tls := out["tls"].(object)
	if out["type"] != "vless" || out["detour"] != "entry" || tls["server_name"] != "example.com" || tls["reality"].(object)["public_key"] != "public-key" || tls["utls"].(object)["fingerprint"] != "chrome" {
		t.Fatal("lost VLESS/REALITY settings")
	}
	if out["transport"] != nil {
		t.Fatal("raw TCP must have no transport wrapper")
	}
}

func TestXrayTransports(t *testing.T) {
	for _, tc := range []struct {
		name, key string
		fields    object
		want      string
	}{
		{"ws", "wsSettings", object{"path": "/ws", "host": "example.com", "maxEarlyData": 2048, "earlyDataHeaderName": "Sec-WebSocket-Protocol"}, "ws"},
		{"httpupgrade", "httpupgradeSettings", object{"path": "/upgrade", "host": "example.com"}, "httpupgrade"},
		{"grpc", "grpcSettings", object{"serviceName": "service"}, "grpc"},
		{"xhttp", "xhttpSettings", object{"host": "example.com", "path": "/x", "mode": "stream-up", "extra": object{"xPaddingBytes": "100-1000", "xmux": object{"maxConcurrency": "16-32"}}}, "xhttp"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := convertXray(xrayFixture(object{"network": tc.name, "security": "none", tc.key: tc.fields}))
			if err != nil {
				t.Fatal(err)
			}
			if out["transport"].(object)["type"] != tc.want {
				t.Fatal("wrong transport")
			}
		})
	}
}

func TestXrayRejectsUntranslatableWireOptions(t *testing.T) {
	for _, stream := range []object{
		{"network": "xdrive"},
		{"network": "grpc", "grpcSettings": object{"multiMode": true}},
		{"network": "raw", "security": "reality", "realitySettings": object{"mldsa65Verify": "key"}},
		{"network": "xhttp", "xhttpSettings": object{"extra": object{"unknownField": "value"}}},
	} {
		if _, err := convertXray(xrayFixture(stream)); err == nil {
			t.Fatal("silently discarded an unsupported option")
		}
	}
	x := xrayFixture(object{"network": "raw"})
	x["outbound"].(object)["mux"] = object{"enabled": true}
	if _, err := convertXray(x); err == nil {
		t.Fatal("Xray mux silently changed wire format")
	}
}

func TestRejectsControllerAsTachyon(t *testing.T) {
	var input object
	_ = json.Unmarshal(fixture(t, []any{object{"type": "http", "tag": "proxy"}}), &input)
	input[ConfigKey] = filepath.Join(t.TempDir(), "ThroneCore.exe")
	data, _ := json.Marshal(input)
	if _, err := PrepareNative(data); err == nil || !strings.Contains(err.Error(), "controller") {
		t.Fatal("selected controller was accepted")
	}
}

func TestReleaseBinaryXrayTransportConfigs(t *testing.T) {
	path := binaryPath(t)
	for _, network := range []string{"raw", "ws", "httpupgrade", "grpc", "xhttp"} {
		t.Run(network, func(t *testing.T) {
			wrapper := xrayFixture(object{"network": network, "security": "tls", "tlsSettings": object{"serverName": "example.com", "fingerprint": "chrome", "alpn": []any{"h2", "http/1.1"}}})
			wrapper["outbound"].(object)["settings"].(object)["flow"] = ""
			native, err := PrepareNative(fixture(t, []any{wrapper}))
			if err != nil {
				t.Fatal(err)
			}
			plan := native.Plan
			plan.Path = path
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := plan.Check(ctx, execLaunch, testFile(t)); err != nil {
				t.Fatal(err)
			}
		})
	}
}
