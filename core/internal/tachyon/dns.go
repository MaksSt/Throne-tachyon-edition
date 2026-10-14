package tachyon

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/miekg/dns"
)

// Throne accepts zone-format records; Tachyon v0.1.1 reads their wire form.
// Use the same parser defaults as Throne without enabling file includes.
func normalizeDNSRecords(config map[string]json.RawMessage) error {
	raw, exists := config["dns"]
	if !exists {
		return nil
	}
	var section map[string]json.RawMessage
	if err := json.Unmarshal(raw, &section); err != nil {
		return err
	}
	rules, exists := section["rules"]
	if !exists {
		return nil
	}
	normalized, err := normalizeDNSRuleRecords(rules, "dns.rules")
	if err != nil {
		return err
	}
	section["rules"] = normalized
	config["dns"], err = json.Marshal(section)
	return err
}

func normalizeDNSRuleRecords(raw json.RawMessage, path string) (json.RawMessage, error) {
	var rules []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rules); err != nil {
		return nil, err
	}
	for index, rule := range rules {
		at := fmt.Sprintf("%s[%d]", path, index)
		if nested, exists := rule["rules"]; exists {
			normalized, err := normalizeDNSRuleRecords(nested, at+".rules")
			if err != nil {
				return nil, err
			}
			rule["rules"] = normalized
		}
		for _, key := range []string{"answer", "ns", "extra", "response_answer", "response_ns", "response_extra"} {
			value, exists := rule[key]
			if !exists {
				continue
			}
			var records []json.RawMessage
			if err := json.Unmarshal(value, &records); err != nil {
				// Throne's Listable fields also accept a single record.
				records = []json.RawMessage{value}
			}
			for i, record := range records {
				var text string
				if json.Unmarshal(record, &text) != nil {
					continue // Leave Tachyon's native object form to its validator.
				}
				if wire, err := base64.StdEncoding.DecodeString(text); err == nil {
					if rr, end, err := dns.UnpackRR(wire, 0); err != nil || rr == nil || len(wire) == 0 || end != len(wire) {
						return nil, fmt.Errorf("%s.%s[%d]: invalid binary DNS record", at, key, i)
					}
					continue
				}
				parser := dns.NewZoneParser(strings.NewReader(text+"\n"), "", "")
				parser.SetDefaultTTL(3600)
				rr, _ := parser.Next()
				if err := parser.Err(); err != nil || rr == nil {
					return nil, fmt.Errorf("%s.%s[%d]: invalid DNS record", at, key, i)
				}
				wire := make([]byte, dns.Len(rr))
				end, err := dns.PackRR(rr, wire, 0, nil, false)
				if err != nil {
					return nil, fmt.Errorf("%s.%s[%d]: encode DNS record: %w", at, key, i, err)
				}
				records[i], err = json.Marshal(wire[:end])
				if err != nil {
					return nil, err
				}
			}
			var err error
			rule[key], err = json.Marshal(records)
			if err != nil {
				return nil, err
			}
		}
	}
	return json.Marshal(rules)
}
