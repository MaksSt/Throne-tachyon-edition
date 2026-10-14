package tachyon

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/miekg/dns"
)

func TestDNSRecordNormalizationPreservesValuesAndTTL(t *testing.T) {
	for _, text := range []string{
		"*. IN A 127.0.0.1", "example.org. 42 IN AAAA ::1",
		"alias.test. 17 IN CNAME target.test.", "txt.test. 123 IN TXT \"some text\"",
	} {
		encoded, _ := json.Marshal(text)
		raw := []byte(`{"dns":{"rules":[{"type":"logical","mode":"and","rules":[{"action":"predefined","answer":` + string(encoded) + `}]}]}}`)
		prepared, err := Prepare(raw, "127.0.0.1:1", "test")
		if err != nil {
			t.Fatal(err)
		}
		var config struct {
			DNS struct {
				Rules []struct {
					Rules []struct{ Answer []string }
				}
			}
		}
		if err = json.Unmarshal(prepared, &config); err != nil {
			t.Fatal(err)
		}
		wire, err := base64.StdEncoding.DecodeString(config.DNS.Rules[0].Rules[0].Answer[0])
		if err != nil {
			t.Fatal(err)
		}
		rr, end, err := dns.UnpackRR(wire, 0)
		if err != nil || end != len(wire) {
			t.Fatalf("invalid wire record: %v", err)
		}
		want, err := dns.NewRR(text)
		if err != nil || rr.String() != want.String() {
			t.Fatalf("record changed: %v, want %v", rr, want)
		}
	}
	for _, text := range []string{"*. IN A invalid", "$INCLUDE private-file", ""} {
		encoded, _ := json.Marshal(text)
		raw := []byte(`{"dns":{"rules":[{"answer":[` + string(encoded) + `]}]}}`)
		if _, err := Prepare(raw, "127.0.0.1:1", "test"); err == nil {
			t.Fatalf("invalid record accepted: %q", text)
		}
	}
}
