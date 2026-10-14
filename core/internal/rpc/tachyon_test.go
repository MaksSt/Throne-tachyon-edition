package rpc

import (
	"context"
	"errors"
	"github.com/miekg/dns"
	"net"
	"os"
	"path/filepath"
	"testing"

	"ThroneCore/gen"
	"ThroneCore/internal/tachyon"
	"google.golang.org/protobuf/proto"
)

func TestBackendSelectionKeepsOriginalSingBox(t *testing.T) {
	originalHooks := tachyonHooks
	t.Cleanup(func() { tachyonHooks = originalHooks })
	path := filepath.Join(t.TempDir(), "tachyon")
	if err := os.WriteFile(path, []byte("test"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("THRONE_TACHYON_PATH", path)
	t.Setenv("THRONE_GUARD", "")
	calls := 0
	tachyonHooks = tachyon.Hooks{
		Config: func(string) (string, string, error) { return "private.json", "private.json", nil },
		Check:  func(context.Context, string, []string) error { calls++; return errors.New("Tachyon selected") },
		Remove: func(string) error { return nil },
	}
	payload, err := proto.Marshal(&gen.LoadConfigReq{CoreConfig: To(`{"outbounds":[{"type":"direct","tag":"proxy"}]}`)})
	if err != nil {
		t.Fatal(err)
	}
	check := func() string {
		data, err := dispatch("CheckConfig", payload)
		if err != nil {
			t.Fatal(err)
		}
		var response gen.ErrorResp
		if err = proto.Unmarshal(data, &response); err != nil {
			t.Fatal(err)
		}
		return response.GetError()
	}
	t.Setenv("THRONE_CORE_BACKEND", "sing-box")
	if message := check(); message != "" || calls != 0 {
		t.Fatalf("original backend changed: %q, calls=%d", message, calls)
	}
	t.Setenv("THRONE_CORE_BACKEND", "tachyon")
	if message := check(); message == "" || calls != 1 {
		t.Fatalf("Tachyon not selected: %q, calls=%d", message, calls)
	}
	t.Setenv("THRONE_CORE_BACKEND", "sing-box")
	if message := check(); message != "" || calls != 1 {
		t.Fatalf("switch back failed: %q, calls=%d", message, calls)
	}
}

func TestTachyonRejectsSidecarsAndKillSwitch(t *testing.T) {
	t.Setenv("THRONE_GUARD", "")
	for _, request := range []*gen.LoadConfigReq{
		{NeedXray: To(true)}, {NeedExtraProcess: To(true)}, {XrayFullConfigs: []string{"{}"}},
	} {
		if err := tachyonRequestError(request); err == nil {
			t.Fatal("unsupported sidecar accepted")
		}
	}
	t.Setenv("THRONE_GUARD", "1")
	if err := tachyonRequestError(&gen.LoadConfigReq{}); err == nil {
		t.Fatal("kill switch accepted")
	}
}

func TestRealTachyonRPCStartStopAndValidation(t *testing.T) {
	path := os.Getenv("TACHYON_TEST_BINARY")
	if path == "" {
		t.Skip("set TACHYON_TEST_BINARY to test the pinned release")
	}
	path, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("THRONE_CORE_BACKEND", "tachyon")
	t.Setenv("THRONE_TACHYON_PATH", path)
	t.Setenv("THRONE_GUARD", "")
	ctx := context.Background()
	defer func() { _, _ = tachyonStop(ctx, &gen.EmptyReq{}) }()
	bad, err := tachyonCheck(ctx, &gen.LoadConfigReq{CoreConfig: To(`{"outbounds":[{"type":"nonexistent"}]}`)})
	if err != nil || bad.GetError() == "" {
		t.Fatalf("invalid config accepted: %v %v", bad, err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	_, port, _ := net.SplitHostPort(address)
	dnsListener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dnsAddress := dnsListener.Addr().String()
	_ = dnsListener.Close()
	_, dnsPort, _ := net.SplitHostPort(dnsAddress)
	// Match the GUI's actual default resolver object and localhost records.
	request := &gen.LoadConfigReq{CoreConfig: To(`{"log":{"level":"warn"},"inbounds":[{"type":"mixed","listen":"127.0.0.1","listen_port":` + port + `},{"type":"direct","tag":"dns-in","listen":"127.0.0.1","listen_port":` + dnsPort + `}],"outbounds":[{"type":"direct","tag":"proxy"}],"dns":{"servers":[{"type":"local","tag":"dns-direct"},{"type":"local","tag":"dns-local"}],"rules":[{"domain":"localhost","query_type":"A","action":"predefined","rcode":"NOERROR","answer":["*. IN A 127.0.0.1"]},{"domain":"localhost","query_type":"AAAA","action":"predefined","rcode":"NXDOMAIN"}],"final":"dns-direct"},"route":{"default_domain_resolver":{"server":"dns-direct","strategy":""},"rules":[{"inbound":"dns-in","action":"hijack-dns"}],"final":"proxy"}}`)}
	for range 2 {
		response, err := tachyonStart(ctx, request)
		if err != nil || response.GetError() != "" {
			t.Fatalf("start: %v %v", response, err)
		}
		if currentBox() != nil || currentTachyon() == nil {
			t.Fatal("wrong data plane")
		}
		query := new(dns.Msg)
		query.SetQuestion("localhost.", dns.TypeA)
		answer, _, err := (&dns.Client{}).ExchangeContext(ctx, query, dnsAddress)
		if err != nil || answer == nil || len(answer.Answer) != 1 {
			t.Fatalf("GUI DNS record did not resolve: %v %v", answer, err)
		}
		if a, ok := answer.Answer[0].(*dns.A); !ok || a.A.String() != "127.0.0.1" || a.Hdr.Ttl != 3600 {
			t.Fatalf("DNS record changed: %v", answer.Answer)
		}
		response, err = tachyonStart(ctx, request)
		if err != nil || response.GetError() == "" {
			t.Fatal("duplicate start accepted")
		}
		if _, err = tachyonConnections(ctx, &gen.EmptyReq{}); err != nil {
			t.Fatal(err)
		}
		response, err = tachyonStop(ctx, &gen.EmptyReq{})
		if err != nil || response.GetError() != "" || currentTachyon() != nil {
			t.Fatal("stop failed")
		}
		if conn, err := net.Dial("tcp", address); err == nil {
			_ = conn.Close()
			t.Fatal("listener survived stop")
		}
	}
}
