package tachyon

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNativePreservesCompleteConfiguration(t *testing.T) {
	var input object
	_ = json.Unmarshal(fixture(t, []any{xrayFixture(object{"network": "raw", "security": "none"})}), &input)
	input["dns"] = object{"servers": []any{object{"type": "local", "tag": "dns-direct"}}, "final": "dns-direct"}
	input["inbounds"] = []any{object{"type": "mixed", "listen": "127.0.0.1", "listen_port": 0}}
	input["route"] = object{"final": "proxy", "default_domain_resolver": object{"server": "dns-direct", "strategy": "prefer_ipv4"}, "rules": []any{object{"domain_suffix": []any{"example.com"}, "outbound": "proxy"}}}
	data, _ := json.Marshal(input)
	native, err := PrepareNative(data)
	if err != nil {
		t.Fatal(err)
	}
	var config object
	_ = json.Unmarshal(native.Plan.Config, &config)
	if config["outbounds"].([]any)[0].(object)["type"] != "vless" {
		t.Fatal("native runtime did not keep protocol outbound")
	}
	if config["dns"].(object)["final"] != "dns-direct" || config["route"].(object)["final"] != "proxy" {
		t.Fatal("DNS/routing policy lost")
	}
	if config["services"] != nil || config[ConfigKey] != nil {
		t.Fatal("controller-only data leaked into native config")
	}
	if len(config["inbounds"].([]any)) != 3 {
		t.Fatal("lost original inbound or probe entrypoint")
	}
}

func TestNativePredefinedDNSRuntime(t *testing.T) {
	path := binaryPath(t)
	var input object
	_ = json.Unmarshal(fixture(t, []any{object{"type": "direct", "tag": "proxy"}}), &input)
	input["dns"] = object{"servers": []any{object{"type": "local", "tag": "dns-direct"}}, "final": "dns-direct", "rules": []any{
		object{"domain": []any{"native.test"}, "query_type": []any{"A"}, "action": "predefined", "answer": []any{"*. 60 IN A 192.0.2.10"}},
		object{"domain": []any{"native.test"}, "query_type": []any{"AAAA"}, "action": "predefined", "answer": []any{"*. 60 IN AAAA 2001:db8::10"}},
	}}
	data, _ := json.Marshal(input)
	native, err := PrepareNative(data)
	if err != nil {
		t.Fatal(err)
	}
	native.Plan.Path = path
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err = native.Start(ctx, execLaunch, testFile(t)); err != nil {
		t.Fatal(err)
	}
	defer native.Stop()
	addresses, err := native.Resolve(ctx, "native.test")
	if err != nil {
		t.Fatal(err)
	}
	if len(addresses) != 2 || addresses[0] != "192.0.2.10" || addresses[1] != "2001:db8::10" {
		t.Fatal("predefined DNS response changed", addresses)
	}
}

func TestNativeValidationWithNullOptionalCollections(t *testing.T) {
	path := binaryPath(t)
	var input object
	_ = json.Unmarshal(fixture(t, []any{object{"type": "direct", "tag": "proxy"}}), &input)
	input["endpoints"] = nil
	input["inbounds"] = nil
	data, _ := json.Marshal(input)
	native, err := PrepareNative(data)
	if err != nil {
		t.Fatal(err)
	}
	native.Plan.Path = path
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err = native.Plan.Check(ctx, execLaunch, testFile(t)); err != nil {
		t.Fatal(err)
	}
}

func TestNativeReleaseRuntimeAndAPI(t *testing.T) {
	path := binaryPath(t)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "native Tachyon runtime") }))
	defer origin.Close()
	data := fixture(t, []any{object{"type": "direct", "tag": "proxy"}})
	native, err := PrepareNative(data)
	if err != nil {
		t.Fatal(err)
	}
	native.Plan.Path = path
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err = native.Start(ctx, execLaunch, testFile(t)); err != nil {
		t.Fatal(err)
	}
	defer native.Stop()
	transport := &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		return native.Dial(ctx, "proxy", address)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	response, err := client.Get(origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || string(body) != "native Tachyon runtime" {
		t.Fatal("native data path failed")
	}
	for _, endpoint := range []string{"/version", "/proxies", "/connections", "/rules", "/configs"} {
		var value object
		if err = native.Call(ctx, "GET", endpoint, nil, &value); err != nil {
			t.Fatalf("%s: %v", endpoint, err)
		}
		var keys []string
		for key := range value {
			keys = append(keys, key)
		}
		t.Log(endpoint, strings.Join(keys, ","))
		if endpoint == "/proxies" {
			if proxies, ok := value["proxies"].(map[string]any); ok {
				if proxy, ok := proxies["proxy"].(map[string]any); ok {
					var keys []string
					for key := range proxy {
						keys = append(keys, key)
					}
					t.Log("proxy fields", strings.Join(keys, ","))
				}
			}
		}
	}
}
