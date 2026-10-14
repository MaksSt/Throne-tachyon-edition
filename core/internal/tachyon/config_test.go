package tachyon

import (
	"encoding/json"
	"testing"
)

func TestPreparePreservesProfileAndReplacesOnlyAPI(t *testing.T) {
	raw := []byte(`{"inbounds":[{"type":"mixed","listen":"127.0.0.1","listen_port":2080}],"outbounds":[{"type":"direct","tag":"proxy"}],"route":{"final":"proxy"},"dns":{"servers":[{"type":"local","tag":"local"}]},"experimental":{"clash_api":{"external_controller":"0.0.0.0:9090","secret":"old"},"cache_file":{"enabled":true}},"services":[{"type":"api","secret":"old"},{"type":"ntp","tag":"clock"}]}`)
	prepared, err := Prepare(raw, "127.0.0.1:4567", "new")
	if err != nil {
		t.Fatal(err)
	}
	var before, after map[string]any
	_ = json.Unmarshal(raw, &before)
	_ = json.Unmarshal(prepared, &after)
	for _, key := range []string{"inbounds", "outbounds", "route", "dns"} {
		original, _ := json.Marshal(before[key])
		current, _ := json.Marshal(after[key])
		if string(original) != string(current) {
			t.Fatalf("%s changed", key)
		}
	}
	exp := after["experimental"].(map[string]any)
	if exp["cache_file"].(map[string]any)["enabled"] != true {
		t.Fatal("cache changed")
	}
	api := exp["clash_api"].(map[string]any)
	if api["external_controller"] != "127.0.0.1:4567" || api["secret"] != "new" {
		t.Fatal("API not replaced")
	}
	services := after["services"].([]any)
	if len(services) != 1 || services[0].(map[string]any)["type"] != "ntp" {
		t.Fatal("non-API service lost")
	}
}

func TestPrepareRejectsUnsupportedAndMalformedConfigurations(t *testing.T) {
	for _, raw := range []string{"null", "[]", "{", `{"inbounds":{}}`, `{"experimental":[]}`, `{"services":{}}`,
		`{"inbounds":[{"type":"tun"}]}`, `{"inbounds":[{"type":"tproxy"}]}`, `{"outbounds":[{"type":"auto_selector"}]}`, `{"endpoints":[{"type":"openvpn"}]}`} {
		t.Run(raw, func(t *testing.T) {
			if _, err := Prepare([]byte(raw), "127.0.0.1:1", "secret"); err == nil {
				t.Fatal("accepted unsafe/invalid config")
			}
		})
	}
}

func TestProbeTagForGeneratedAndCustomConfigurations(t *testing.T) {
	for _, test := range []struct{ raw, want string }{
		{`{"route":{"final":"direct"},"outbounds":[{"tag":"proxy"},{"tag":"direct"}]}`, "proxy"},
		{`{"route":{"final":"exit"},"outbounds":[{"tag":"direct"},{"tag":"exit"}]}`, "exit"},
		{`{"outbounds":[{"tag":"custom"}]}`, "custom"},
		{`{}`, ""},
	} {
		if got := ProbeTag([]byte(test.raw)); got != test.want {
			t.Fatalf("probe tag %q, want %q", got, test.want)
		}
	}
}

func TestThroneResolverObjectsAreNormalizedWithoutDroppingOptions(t *testing.T) {
	raw := []byte(`{"route":{"default_domain_resolver":{"server":"dns-direct","strategy":""}},"outbounds":[{"type":"direct","domain_resolver":{"server":"dns-direct","strategy":"as_is"}}],"dns":{"servers":[{"type":"local","tag":"dns-direct","domain_resolver":{"server":"dns-local"}},{"type":"local","tag":"dns-local"}]}}`)
	prepared, err := Prepare(raw, "127.0.0.1:1", "test")
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err = json.Unmarshal(prepared, &config); err != nil {
		t.Fatal(err)
	}
	if config["route"].(map[string]any)["default_domain_resolver"] != "dns-direct" {
		t.Fatal("default resolver not normalized")
	}
	if config["outbounds"].([]any)[0].(map[string]any)["domain_resolver"] != "dns-direct" {
		t.Fatal("outbound resolver not normalized")
	}
	if config["dns"].(map[string]any)["servers"].([]any)[0].(map[string]any)["domain_resolver"] != "dns-local" {
		t.Fatal("DNS resolver not normalized")
	}
	for _, options := range []string{`"strategy":"prefer_ipv4"`, `"disable_cache":true`, `"rewrite_ttl":0`, `"client_subnet":"10.0.0.0/8"`} {
		config := `{"route":{"default_domain_resolver":{"server":"dns-direct",` + options + `}}}`
		if _, err = Prepare([]byte(config), "127.0.0.1:1", "test"); err == nil {
			t.Fatal("resolver query option silently discarded")
		}
	}
}
