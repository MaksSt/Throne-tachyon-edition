package tachyon

import (
	"context"
	"encoding/base64"
	"fmt"
	"github.com/miekg/dns"
)

func (n *Native) Resolve(ctx context.Context, domain string) ([]string, error) {
	if n.DNSAddress == "" {
		return nil, fmt.Errorf("Tachyon configuration has no DNS section")
	}
	var addresses []string
	for _, kind := range []uint16{dns.TypeA, dns.TypeAAAA} {
		query := &dns.Msg{}
		query.SetQuestion(dns.Fqdn(domain), kind)
		response, _, err := (&dns.Client{Net: "udp"}).ExchangeContext(ctx, query, n.DNSAddress)
		if err != nil {
			return nil, err
		}
		if response.Rcode != dns.RcodeSuccess {
			return nil, fmt.Errorf("Tachyon DNS returned %s", dns.RcodeToString[response.Rcode])
		}
		for _, answer := range response.Answer {
			switch rr := answer.(type) {
			case *dns.A:
				addresses = append(addresses, rr.A.String())
			case *dns.AAAA:
				addresses = append(addresses, rr.AAAA.String())
			}
		}
	}
	return addresses, nil
}

// Throne stores RFC 1035 presentation records; Tachyon takes base64 wire RRs.
func normalizeDNSRules(rules []any) error {
	for _, value := range rules {
		rule, ok := value.(object)
		if !ok {
			continue
		}
		if children, _ := rule["rules"].([]any); children != nil {
			if err := normalizeDNSRules(children); err != nil {
				return err
			}
		}
		if rule["action"] != "predefined" {
			continue
		}
		for _, section := range []string{"answer", "ns", "extra"} {
			records, _ := rule[section].([]any)
			for i, value := range records {
				text, ok := value.(string)
				if !ok {
					return fmt.Errorf("tachyon-core: DNS %s entry must be a record", section)
				}
				rr, err := dns.NewRR(text)
				if err != nil {
					if _, err := base64.StdEncoding.DecodeString(text); err == nil {
						continue
					}
					return fmt.Errorf("tachyon-core: invalid DNS %s record %d", section, i)
				}
				wire := make([]byte, 65535)
				end, err := dns.PackRR(rr, wire, 0, nil, false)
				if err != nil {
					return fmt.Errorf("tachyon-core: encode DNS %s: %w", section, err)
				}
				records[i] = base64.StdEncoding.EncodeToString(wire[:end])
			}
		}
	}
	return nil
}
