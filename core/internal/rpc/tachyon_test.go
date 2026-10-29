package rpc

import (
	"ThroneCore/gen"
	"context"
	"encoding/json"
	"google.golang.org/protobuf/proto"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestNativeRPCNeverCreatesSingBox(t *testing.T) {
	path := os.Getenv("TACHYON_BINARY")
	if path == "" {
		t.Skip("set TACHYON_BINARY")
	}
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "native-only") }))
	defer origin.Close()
	config, _ := json.Marshal(map[string]any{"_throne_tachyon": path, "outbounds": []any{map[string]any{"type": "direct", "tag": "proxy"}}, "route": map[string]any{"final": "proxy"}})
	s := &server{}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	response, err := s.Start(ctx, &gen.LoadConfigReq{CoreConfig: To(string(config))})
	if err != nil || response.GetError() != "" {
		t.Fatalf("native Start: %v %s", err, response.GetError())
	}
	defer s.Stop(context.Background(), &gen.EmptyReq{})
	if currentBox() != nil || currentNative() == nil {
		t.Fatal("Tachyon selection started a sing-box instance")
	}
	native := currentNative()
	transport := &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		return native.engine.Dial(ctx, "proxy", address)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	res, err := client.Get(origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if string(body) != "native-only" {
		t.Fatal("wrong response")
	}
	stats, err := s.QueryStats(ctx, &gen.EmptyReq{})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Downs["__native_total"] <= 0 {
		t.Fatal("native statistics were not reported")
	}
	second, err := s.QueryStats(ctx, &gen.EmptyReq{})
	if err != nil || second.Downs["__native_total"] != 0 {
		t.Fatal("native cumulative counter was double-counted", err)
	}
	if _, err := s.QueryConnections(ctx, &gen.EmptyReq{}); err != nil {
		t.Fatal(err)
	}
	echo, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		c, e := echo.Accept()
		if e == nil {
			defer c.Close()
			_, _ = io.Copy(c, c)
		}
	}()
	stream, err := native.engine.Dial(ctx, "proxy", echo.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	_, _ = stream.Write([]byte("hello"))
	buf := make([]byte, 5)
	if _, err = io.ReadFull(stream, buf); err != nil {
		t.Fatal(err)
	}
	connections, err := s.QueryConnections(ctx, &gen.EmptyReq{})
	if err != nil || len(connections.Active) == 0 {
		t.Fatal("native live connections unavailable", err)
	}
	previous := connections.Active[len(connections.Active)-1]
	if _, err := proto.Marshal(previous); err != nil {
		t.Fatal(err)
	}
	closed, err := s.CloseConnections(ctx, &gen.CloseConnectionsRequest{Ids: []string{connections.Active[len(connections.Active)-1].GetId()}})
	if err != nil || closed.GetError() != "" || closed.GetClosed() != 1 {
		t.Fatal("native connection close failed", err, closed.GetError())
	}
	history, err := s.QueryConnections(ctx, &gen.EmptyReq{})
	if err != nil {
		t.Fatal(err)
	}
	var retained *gen.ConnectionMetaData
	for _, entry := range history.Closed {
		if entry.GetId() == previous.GetId() {
			retained = entry
			break
		}
	}
	if retained == nil || retained.GetClosedAt() == 0 || previous.GetClosedAt() != 0 {
		t.Fatal("closed history lost the connection or changed a published snapshot")
	}
	if _, err := proto.Marshal(retained); err != nil {
		t.Fatal(err)
	}
	if updated, err := s.UpdateRuleSets(ctx, &gen.EmptyReq{}); err != nil || updated.GetError() != "" {
		t.Fatal("native provider API failed", err, updated.GetError())
	}
	if env, err := prepareTestEnv(true, false, "", nil, "", nil, true, ""); err != nil || env.box != native {
		t.Fatal("current test fell back to sing-box", err)
	}
	if _, err := s.Stop(ctx, &gen.EmptyReq{}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-native.engine.Session.Done():
	default:
		t.Fatal("native engine survived Stop")
	}
	plain, _ := json.Marshal(map[string]any{"outbounds": []any{map[string]any{"type": "direct", "tag": "proxy"}}, "route": map[string]any{"final": "proxy"}})
	response, err = s.Start(ctx, &gen.LoadConfigReq{CoreConfig: To(string(plain)), NeedXray: To(false), NeedExtraProcess: To(false)})
	if err != nil || response.GetError() != "" {
		t.Fatal("switch back to default backend failed", err, response.GetError())
	}
	if currentNative() != nil || currentBox() == nil {
		t.Fatal("backend switch left the wrong runtime active")
	}
}

func TestNativeSelectorPinAndRelease(t *testing.T) {
	path := os.Getenv("TACHYON_BINARY")
	if path == "" {
		t.Skip("set TACHYON_BINARY")
	}
	config, _ := json.Marshal(map[string]any{"_throne_tachyon": path, "outbounds": []any{
		map[string]any{"type": "auto-selector", "tag": "proxy", "outbounds": []string{"one", "two"}, "interval": "10m"},
		map[string]any{"type": "direct", "tag": "one"}, map[string]any{"type": "direct", "tag": "two"}}, "route": map[string]any{"final": "proxy"}})
	s := &server{}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	response, err := s.Start(ctx, &gen.LoadConfigReq{CoreConfig: To(string(config))})
	if err != nil || response.GetError() != "" {
		t.Fatal(err, response.GetError())
	}
	defer s.Stop(context.Background(), &gen.EmptyReq{})
	for _, member := range []string{"two", ""} {
		response, err = s.AutoSelectorAction(ctx, &gen.AutoSelectorActionRequest{Tag: To("proxy"), Action: To("select"), Member: To(member)})
		if err != nil || response.GetError() != "" {
			t.Fatal("native pin/release failed", err, response.GetError())
		}
		status, err := s.QueryAutoSelectors(ctx, &gen.EmptyReq{})
		if err != nil || len(status.Groups) != 1 {
			t.Fatal("native selector status unavailable", err)
		}
		if status.Groups[0].GetPinned() != member {
			t.Fatal("native pin state is wrong")
		}
	}
}
