package rpc

import (
	"context"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"ThroneCore/gen"
	"ThroneCore/internal/probe"
	"ThroneCore/internal/process"
	"ThroneCore/internal/tachyon"
	"github.com/sagernet/sing-box/adapter"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/service"
	"google.golang.org/protobuf/proto"
)

type nativeProbeBox struct {
	engine           *tachyon.Native
	ctx              context.Context
	manager          *nativeOutbounds
	mu               sync.Mutex
	previous         map[string]*gen.ConnectionMetaData
	closed           []*gen.ConnectionMetaData
	statsMu          sync.Mutex
	lastUp, lastDown int64
}
type nativeOutbounds struct {
	adapter.OutboundManager
	entries    map[string]adapter.Outbound
	defaultTag string
}
type nativeOutbound struct {
	engine *tachyon.Native
	tag    string
}

func (o *nativeOutbound) Type() string           { return "tachyon" }
func (o *nativeOutbound) Tag() string            { return o.tag }
func (o *nativeOutbound) Network() []string      { return []string{"tcp"} }
func (o *nativeOutbound) Dependencies() []string { return nil }
func (o *nativeOutbound) DialContext(ctx context.Context, _ string, destination M.Socksaddr) (net.Conn, error) {
	return o.engine.Dial(ctx, o.tag, destination.String())
}
func (o *nativeOutbound) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, fmt.Errorf("Tachyon probe entrypoints are TCP-only; UDP traffic is handled by the native runtime")
}
func (m *nativeOutbounds) Outbounds() []adapter.Outbound {
	var out []adapter.Outbound
	for _, v := range m.entries {
		out = append(out, v)
	}
	return out
}
func (m *nativeOutbounds) Outbound(tag string) (adapter.Outbound, bool) {
	o, ok := m.entries[tag]
	return o, ok
}
func (m *nativeOutbounds) Default() adapter.Outbound        { return m.entries[m.defaultTag] }
func (b *nativeProbeBox) Context() context.Context          { return b.ctx }
func (b *nativeProbeBox) Outbound() adapter.OutboundManager { return b.manager }

func nativeConfigFile(content string) (string, func(), error) {
	path, cleanup, err := process.CreateExtraConfig(content)
	return path, func() {
		if cleanup != "" {
			_ = os.RemoveAll(cleanup)
		}
	}, err
}
func launchNativeEngine(path string, args []string) (tachyon.Child, error) {
	return process.StartEngine(path, args)
}
func newNativeBox(ctx context.Context, content string) (*nativeProbeBox, error) {
	engine, err := tachyon.PrepareNativeWithInterface([]byte(content), defaultInterfaceFinder())
	if err != nil {
		return nil, err
	}
	startCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err = engine.Start(startCtx, launchNativeEngine, nativeConfigFile); err != nil {
		return nil, err
	}
	m := &nativeOutbounds{entries: map[string]adapter.Outbound{}, defaultTag: engine.DefaultTag}
	for tag := range engine.Probes {
		m.entries[tag] = &nativeOutbound{engine: engine, tag: tag}
	}
	boxCtx := service.ContextWithDefaultRegistry(context.Background())
	boxCtx = service.ContextWith[adapter.OutboundManager](boxCtx, m)
	return &nativeProbeBox{engine: engine, ctx: boxCtx, manager: m, previous: map[string]*gen.ConnectionMetaData{}}, nil
}

func currentNative() *nativeProbeBox    { stateMu.RLock(); defer stateMu.RUnlock(); return nativeInstance }
func publishNative(box *nativeProbeBox) { stateMu.Lock(); nativeInstance = box; stateMu.Unlock() }

type clashConnection struct {
	ID          string    `json:"id"`
	Upload      int64     `json:"upload"`
	Download    int64     `json:"download"`
	Start       time.Time `json:"start"`
	Chains      []string  `json:"chains"`
	Rule        string    `json:"rule"`
	RulePayload string    `json:"rulePayload"`
	Metadata    struct {
		Network         string `json:"network"`
		Type            string `json:"type"`
		Host            string `json:"host"`
		DestinationIP   string `json:"destinationIP"`
		DestinationPort string `json:"destinationPort"`
		SourceIP        string `json:"sourceIP"`
		SourcePort      string `json:"sourcePort"`
		Process         string `json:"process"`
		ProcessPath     string `json:"processPath"`
	} `json:"metadata"`
}
type clashConnections struct {
	UploadTotal   int64             `json:"uploadTotal"`
	DownloadTotal int64             `json:"downloadTotal"`
	Connections   []clashConnection `json:"connections"`
}

func (b *nativeProbeBox) connections(ctx context.Context) (*gen.QueryConnectionsResp, *clashConnections, error) {
	var snapshot clashConnections
	if err := b.engine.Call(ctx, "GET", "/connections", nil, &snapshot); err != nil {
		return nil, nil, err
	}
	result := &gen.QueryConnectionsResp{}
	current := map[string]*gen.ConnectionMetaData{}
	for _, c := range snapshot.Connections {
		m := c.Metadata
		tag := b.engine.DefaultTag
		if len(c.Chains) > 0 {
			tag = c.Chains[0]
		}
		dest := m.DestinationIP
		if dest == "" {
			dest = m.Host
		}
		p := &gen.ConnectionMetaData{Id: To(c.ID), CreatedAt: To(c.Start.UnixMilli()), Upload: To(c.Upload), Download: To(c.Download), Outbound: To(tag), Network: To(m.Network), Dest: To(net.JoinHostPort(dest, m.DestinationPort)), Domain: To(m.Host), Protocol: To(m.Type), Process: To(m.Process), ProcessPath: To(m.ProcessPath), Chain: c.Chains, Source: To(net.JoinHostPort(m.SourceIP, m.SourcePort)), Rule: To(c.Rule), RuleItem: To(c.RulePayload)}
		result.Active = append(result.Active, p)
		current[c.ID] = p
	}
	b.mu.Lock()
	for id, old := range b.previous {
		if current[id] == nil {
			closed := proto.Clone(old).(*gen.ConnectionMetaData)
			closed.ClosedAt = To(time.Now().UnixMilli())
			b.closed = append(b.closed, closed)
		}
	}
	b.previous = current
	for len(b.closed) > 256 || (len(b.closed) > 0 && b.closed[0].GetClosedAt() < time.Now().Add(-10*time.Second).UnixMilli()) {
		b.closed = b.closed[1:]
	}
	result.Closed = append(result.Closed, b.closed...)
	b.mu.Unlock()
	return result, &snapshot, nil
}

func (b *nativeProbeBox) stats(ctx context.Context) (*gen.QueryStatsResp, error) {
	b.statsMu.Lock()
	defer b.statsMu.Unlock()
	var snapshot clashConnections
	if err := b.engine.Call(ctx, "GET", "/connections", nil, &snapshot); err != nil {
		return nil, err
	}
	up, down := snapshot.UploadTotal-b.lastUp, snapshot.DownloadTotal-b.lastDown
	if up < 0 {
		up = snapshot.UploadTotal
	}
	if down < 0 {
		down = snapshot.DownloadTotal
	}
	b.lastUp, b.lastDown = snapshot.UploadTotal, snapshot.DownloadTotal
	// Clash supplies exact global totals, not per-outbound totals. Keep the
	// distinction in the RPC contract instead of charging direct traffic to a proxy.
	return &gen.QueryStatsResp{Ups: map[string]int64{"__native_total": up}, Downs: map[string]int64{"__native_total": down}}, nil
}

var _ probe.Box = (*nativeProbeBox)(nil)
