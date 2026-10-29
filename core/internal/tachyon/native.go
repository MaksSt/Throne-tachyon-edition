package tachyon

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"time"
)

// Native runs the entire configuration in Tachyon. There is no sing-box box
// and no inter-engine SOCKS bridge. Private listeners are only probe entrypoints.
type Native struct {
	Plan         *Plan
	Probes       map[string]Bridge
	DefaultTag   string
	Controller   string
	Secret       string
	DNSAddress   string
	Session      *Session
	Selectors    map[string][]string
	SelectorAuto map[string]string
	client       *http.Client
}

func Selected(content []byte) bool { return bytes.Contains(content, []byte(`"`+ConfigKey+`"`)) }

func PrepareNative(content []byte) (*Native, error) {
	return PrepareNativeWithInterface(content, "")
}

// The host supplies the physical interface from its always-on network monitor.
// Tachyon's Windows auto_route does not protect its own outbound sockets.
func PrepareNativeWithInterface(content []byte, physicalInterface string) (*Native, error) {
	var config object
	if err := json.Unmarshal(content, &config); err != nil {
		return nil, err
	}
	path, _ := config[ConfigKey].(string)
	if path == "" {
		return nil, fmt.Errorf("tachyon-core: select an executable in Basic Settings")
	}
	if err := validateExecutable(path); err != nil {
		return nil, err
	}
	delete(config, ConfigKey)
	// Qt and sing-box accept null for absent optional collections; Tachyon's
	// strict decoder requires an array or an omitted field.
	for _, section := range []string{"inbounds", "outbounds", "endpoints"} {
		if config[section] == nil {
			delete(config, section)
		}
	}
	if dns, _ := config["dns"].(object); dns != nil {
		rules, _ := dns["rules"].([]any)
		if err := normalizeDNSRules(rules); err != nil {
			return nil, err
		}
	}
	n := &Native{Plan: &Plan{Path: path}, Probes: map[string]Bridge{}, Selectors: map[string][]string{}, SelectorAuto: map[string]string{}, client: &http.Client{Timeout: 5 * time.Second}}
	var reservations []io.Closer
	defer func() {
		for _, listener := range reservations {
			_ = listener.Close()
		}
	}()
	port := func(udp bool) (int, error) {
		p, sockets, err := reserveNativePort(udp)
		reservations = append(reservations, sockets...)
		return p, err
	}
	route, _ := config["route"].(object)
	if route == nil {
		route = object{}
		config["route"] = route
	}
	resolver, strategy, err := resolverFields(route["default_domain_resolver"])
	if err != nil {
		return nil, err
	}
	if resolver != "" {
		route["default_domain_resolver"] = resolver
	}
	var allTags []string
	usedTags := map[string]bool{}
	for _, section := range []string{"outbounds", "endpoints"} {
		items, _ := config[section].([]any)
		for _, value := range items {
			if item, ok := value.(object); ok {
				if tag, ok := item["tag"].(string); ok {
					usedTags[tag] = true
				}
			}
		}
	}
	for _, section := range []string{"outbounds", "endpoints"} {
		items, _ := config[section].([]any)
		for i, value := range items {
			out, ok := value.(object)
			if !ok {
				return nil, fmt.Errorf("tachyon-core: invalid %s entry", section)
			}
			if out["type"] == "_throne_xray" {
				out, err = convertXray(out)
				if err != nil {
					return nil, err
				}
				items[i] = out
			}
			tag, _ := out["tag"].(string)
			if tag == "" {
				tag = fmt.Sprintf("throne-%s-%d", section, i)
				out["tag"] = tag
			}
			own, ownStrategy, e := resolverFields(out["domain_resolver"])
			if e != nil {
				return nil, e
			}
			if own != "" {
				out["domain_resolver"] = own
			}
			if ownStrategy == "" {
				ownStrategy = strategy
			}
			if _, set := out["domain_strategy"]; !set && ownStrategy != "" {
				out["domain_strategy"] = ownStrategy
			}
			if out["type"] == "auto-selector" {
				members, _ := out["outbounds"].([]any)
				var tags []string
				for _, v := range members {
					if s, ok := v.(string); ok {
						tags = append(tags, s)
					}
				}
				n.Selectors[tag] = tags
				autoTag := fmt.Sprintf("throne-auto-%d", i)
				for usedTags[autoTag] {
					autoTag += "-auto"
				}
				usedTags[autoTag] = true
				n.SelectorAuto[tag] = autoTag
				// Tachyon owns URL probing and selection in this mode.
				converted := object{"type": "urltest", "tag": autoTag, "outbounds": members}
				for _, key := range []string{"url", "interval", "tolerance", "interrupt_exist_connections"} {
					if v, ok := out[key]; ok {
						converted[key] = v
					}
				}
				items = append(items, converted)
				choices := append([]any{autoTag}, members...)
				out = object{"type": "selector", "tag": tag, "outbounds": choices, "default": autoTag}
				items[i] = out
			}
			if out["type"] != "block" && out["type"] != "dns" {
				allTags = append(allTags, tag)
			}
		}
		if items != nil {
			config[section] = items
		}
	}
	if len(allTags) == 0 {
		return nil, fmt.Errorf("tachyon-core: configuration has no usable outbound")
	}
	if runtime.GOOS == "windows" {
		if err := pinWindowsTUNEgress(config, physicalInterface); err != nil {
			return nil, err
		}
	}
	n.DefaultTag, _ = route["final"].(string)
	if n.DefaultTag == "" {
		n.DefaultTag = allTags[0]
	}
	inbounds, _ := config["inbounds"].([]any)
	rules, _ := route["rules"].([]any)
	var probeRules []any
	for i, tag := range allTags {
		p, e := port(false)
		if e != nil {
			return nil, e
		}
		var random [32]byte
		if _, e = rand.Read(random[:]); e != nil {
			return nil, e
		}
		user, password := hex.EncodeToString(random[:16]), hex.EncodeToString(random[16:])
		b := Bridge{Address: net.JoinHostPort("127.0.0.1", fmt.Sprint(p)), Username: user, Password: password}
		n.Probes[tag] = b
		n.Plan.Bridges = append(n.Plan.Bridges, b)
		inTag := fmt.Sprintf("throne-probe-%d", i)
		inbounds = append(inbounds, object{"type": "socks", "tag": inTag, "listen": "127.0.0.1", "listen_port": p, "users": []any{object{"username": user, "password": password}}})
		probeRules = append(probeRules, object{"inbound": []string{inTag}, "action": "route", "outbound": tag})
	}
	config["inbounds"] = inbounds
	route["rules"] = append(probeRules, rules...)
	if _, hasDNS := config["dns"]; hasDNS {
		dnsPort, e := port(true)
		if e != nil {
			return nil, e
		}
		n.DNSAddress = net.JoinHostPort("127.0.0.1", fmt.Sprint(dnsPort))
		config["inbounds"] = append(inbounds, object{"type": "direct", "tag": "throne-native-dns", "listen": "127.0.0.1", "listen_port": dnsPort, "override_address": "8.8.8.8", "override_port": 53})
		route["rules"] = append([]any{object{"inbound": []string{"throne-native-dns"}, "action": "hijack-dns"}}, route["rules"].([]any)...)
	}
	// The sing-box API service has no equivalent in Tachyon; Throne's own UI and
	// IPC use the authenticated Clash API below for statistics and connections.
	delete(config, "services")
	experimental, _ := config["experimental"].(object)
	if experimental == nil {
		experimental = object{}
		config["experimental"] = experimental
	}
	clash, _ := experimental["clash_api"].(object)
	if clash == nil {
		clash = object{}
	}
	controller, _ := clash["external_controller"].(string)
	if controller == "" {
		p, e := port(false)
		if e != nil {
			return nil, e
		}
		controller = net.JoinHostPort("127.0.0.1", fmt.Sprint(p))
		clash["external_controller"] = controller
	}
	secret, _ := clash["secret"].(string)
	if secret == "" {
		var key [32]byte
		if _, e := rand.Read(key[:]); e != nil {
			return nil, e
		}
		secret = hex.EncodeToString(key[:])
		clash["secret"] = secret
	}
	experimental["clash_api"] = clash
	n.Secret = secret
	host, p, e := net.SplitHostPort(controller)
	if e != nil {
		return nil, fmt.Errorf("tachyon-core: invalid Clash controller address")
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	n.Controller = "http://" + net.JoinHostPort(host, p)
	n.Plan.Config, err = json.Marshal(config)
	return n, err
}

func (n *Native) Start(ctx context.Context, launch Launch, file ConfigFile) error {
	s, err := n.Plan.Start(ctx, launch, file)
	if err != nil {
		return err
	}
	n.Session = s
	for {
		if err = n.Call(ctx, "GET", "/version", nil, nil); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			s.Stop()
			return fmt.Errorf("tachyon-core API readiness: %w", ctx.Err())
		case <-s.Done():
			s.Stop()
			return fmt.Errorf("tachyon-core exited before API readiness")
		case <-time.After(30 * time.Millisecond):
		}
	}
}
func (n *Native) Stop() {
	if n.Session != nil {
		n.Session.Stop()
	}
	n.client.CloseIdleConnections()
}
func (n *Native) Call(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, n.Controller+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+n.Secret)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := n.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("tachyon-core API %s %s: HTTP %d", method, path, resp.StatusCode)
	}
	if out != nil {
		return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out)
	}
	_, err = io.Copy(io.Discard, resp.Body)
	return err
}
func ProxyPath(tag string) string { return "/proxies/" + url.PathEscape(tag) }

// Dial only opens a TCP stream through this Tachyon instance's tagged probe
// inbound. Resolution and the complete outbound chain run inside Tachyon.
func (n *Native) Dial(ctx context.Context, tag, address string) (net.Conn, error) {
	b, ok := n.Probes[tag]
	if !ok {
		return nil, fmt.Errorf("tachyon-core: no outbound %s", tag)
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", b.Address)
	if err != nil {
		return nil, err
	}
	okay := false
	defer func() {
		if !okay {
			_ = conn.Close()
		}
	}()
	deadline := time.Now().Add(15 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)
	if _, err = conn.Write([]byte{5, 1, 2}); err != nil {
		return nil, err
	}
	var reply [2]byte
	if _, err = io.ReadFull(conn, reply[:]); err != nil {
		return nil, err
	}
	if reply != [2]byte{5, 2} {
		return nil, fmt.Errorf("tachyon-core: SOCKS authentication unavailable")
	}
	auth := append([]byte{1, byte(len(b.Username))}, b.Username...)
	auth = append(auth, byte(len(b.Password)))
	auth = append(auth, b.Password...)
	if _, err = conn.Write(auth); err != nil {
		return nil, err
	}
	if _, err = io.ReadFull(conn, reply[:]); err != nil {
		return nil, err
	}
	if reply != [2]byte{1, 0} {
		return nil, fmt.Errorf("tachyon-core: SOCKS authentication failed")
	}
	host, p, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	var port int
	if _, err = fmt.Sscan(p, &port); err != nil {
		return nil, err
	}
	if len(host) > 255 {
		return nil, fmt.Errorf("destination hostname too long")
	}
	request := []byte{5, 1, 0, 3, byte(len(host))}
	request = append(request, host...)
	request = append(request, byte(port>>8), byte(port))
	if _, err = conn.Write(request); err != nil {
		return nil, err
	}
	var header [4]byte
	if _, err = io.ReadFull(conn, header[:]); err != nil {
		return nil, err
	}
	if header[1] != 0 {
		return nil, fmt.Errorf("tachyon-core: outbound connect rejected (SOCKS %d)", header[1])
	}
	size := 0
	switch header[3] {
	case 1:
		size = 4
	case 4:
		size = 16
	case 3:
		var length [1]byte
		if _, err = io.ReadFull(conn, length[:]); err != nil {
			return nil, err
		}
		size = int(length[0])
	default:
		return nil, fmt.Errorf("invalid SOCKS response")
	}
	if _, err = io.CopyN(io.Discard, conn, int64(size+2)); err != nil {
		return nil, err
	}
	_ = conn.SetDeadline(time.Time{})
	okay = true
	return conn, nil
}

func (n *Native) IsProbe(tag string) bool { return strings.HasPrefix(tag, "throne-probe-") }
