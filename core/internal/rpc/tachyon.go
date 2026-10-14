package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"ThroneCore/gen"
	"ThroneCore/internal/probe"
	"ThroneCore/internal/process"
	"ThroneCore/internal/tachyon"
)

var tachyonInstance *tachyon.Runtime // guarded by stateMu; writes also hold lifecycleMu
var tachyonDisconnected bool         // guarded by lifecycleMu

var tachyonHooks = tachyon.Hooks{
	Config: process.CreateExtraConfig,
	Remove: os.RemoveAll,
	Check:  process.RunCommand,
	Launch: func(path string, args []string, cleanup string) (tachyon.Child, error) {
		child := process.NewManagedProcess(path, args, false)
		child.SetCleanupPath(cleanup)
		return child, child.Start()
	},
}

func tachyonSelected() bool { return os.Getenv("THRONE_CORE_BACKEND") == "tachyon" }

func currentTachyon() *tachyon.Runtime {
	stateMu.RLock()
	defer stateMu.RUnlock()
	return tachyonInstance
}

func setTachyon(instance *tachyon.Runtime) {
	stateMu.Lock()
	defer stateMu.Unlock()
	tachyonInstance = instance
}

func tachyonRequestError(in *gen.LoadConfigReq) error {
	if in.GetNeedXray() || len(in.GetXrayFullConfigs()) > 0 {
		return errors.New("Tachyon backend does not support Xray sidecars; select built-in sing-box")
	}
	if in.GetNeedExtraProcess() {
		return errors.New("Tachyon backend does not support Extra Core; select built-in sing-box")
	}
	if os.Getenv("THRONE_GUARD") == "1" {
		return errors.New("Tachyon backend does not support Throne kill switch; select built-in sing-box")
	}
	return nil
}

func tachyonError(err error) *gen.ErrorResp {
	response := &gen.ErrorResp{}
	if err != nil {
		response.Error = To(err.Error())
	}
	return response
}

func tachyonStart(ctx context.Context, in *gen.LoadConfigReq) (*gen.ErrorResp, error) {
	lifecycleMu.Lock()
	defer lifecycleMu.Unlock()
	if tachyonDisconnected {
		return tachyonError(errors.New("GUI disconnected")), nil
	}
	if currentTachyon() != nil {
		return tachyonError(errors.New("instance already started")), nil
	}
	if err := tachyonRequestError(in); err != nil {
		return tachyonError(err), nil
	}
	instance, err := tachyon.Start(ctx, tachyonHooks, os.Getenv("THRONE_TACHYON_PATH"), []byte(in.GetCoreConfig()))
	if err == nil {
		setTachyon(instance)
	}
	return tachyonError(err), nil
}

func tachyonStop(context.Context, *gen.EmptyReq) (*gen.ErrorResp, error) {
	lifecycleMu.Lock()
	defer lifecycleMu.Unlock()
	instance := currentTachyon()
	setTachyon(nil)
	if instance != nil {
		instance.Close()
	}
	return &gen.ErrorResp{}, nil
}

func tachyonCheck(ctx context.Context, in *gen.LoadConfigReq) (*gen.ErrorResp, error) {
	if err := tachyonRequestError(in); err != nil {
		return tachyonError(err), nil
	}
	return tachyonError(tachyon.Check(ctx, tachyonHooks, os.Getenv("THRONE_TACHYON_PATH"), []byte(in.GetCoreConfig()))), nil
}

func tachyonConnections(ctx context.Context, _ *gen.EmptyReq) (*gen.QueryConnectionsResp, error) {
	response := &gen.QueryConnectionsResp{}
	instance := currentTachyon()
	if instance == nil {
		return response, nil
	}
	snapshot, err := instance.Client().Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	for _, conn := range snapshot.Connections {
		metadata := conn.Metadata
		host := metadata.DestinationIP
		if host == "" {
			host = metadata.Host
		}
		outbound := ""
		processName := ""
		if metadata.ProcessPath != "" {
			processName = filepath.Base(metadata.ProcessPath)
		}
		if len(conn.Chains) > 0 {
			outbound = conn.Chains[len(conn.Chains)-1]
		}
		response.Active = append(response.Active, &gen.ConnectionMetaData{
			Id: To(conn.ID), CreatedAt: To(conn.Start.UnixMilli()), Upload: To(conn.Upload), Download: To(conn.Download),
			Outbound: To(outbound), Network: To(metadata.Network), Dest: To(net.JoinHostPort(host, metadata.DestinationPort)),
			Domain: To(metadata.Host), Process: To(processName), ProcessPath: To(metadata.ProcessPath),
			Chain: conn.Chains, Source: To(net.JoinHostPort(metadata.SourceIP, metadata.SourcePort)),
		})
	}
	return response, nil
}

func tachyonCloseConnections(ctx context.Context, in *gen.CloseConnectionsRequest) (*gen.CloseConnectionsResponse, error) {
	instance := currentTachyon()
	if instance == nil {
		return &gen.CloseConnectionsResponse{Error: To(errInstanceNotRunning.Error())}, nil
	}
	var closed int32
	for _, id := range in.Ids {
		wasClosed, err := instance.Client().CloseConnection(ctx, id)
		if err != nil {
			return &gen.CloseConnectionsResponse{Closed: To(closed), Error: To(err.Error())}, nil
		}
		if wasClosed {
			closed++
		}
	}
	return &gen.CloseConnectionsResponse{Closed: To(closed)}, nil
}

var tachyonTests struct {
	sync.Mutex
	cancel context.CancelFunc
}

func tachyonTest(ctx context.Context, in *gen.TestReq) (*gen.TestResp, error) {
	if in.GetNeedXray() || len(in.GetXrayFullConfigs()) > 0 {
		return nil, errors.New("Tachyon URL tests do not support Xray sidecars")
	}
	tachyonTests.Lock()
	if tachyonTests.cancel != nil {
		tachyonTests.Unlock()
		return nil, errors.New("Tachyon URL test already running")
	}
	ctx, cancel := context.WithCancel(ctx)
	tachyonTests.cancel = cancel
	tachyonTests.Unlock()
	defer func() { cancel(); tachyonTests.Lock(); tachyonTests.cancel = nil; tachyonTests.Unlock() }()
	instance := currentTachyon()
	if !in.GetTestCurrent() {
		var err error
		instance, err = tachyon.Start(ctx, tachyonHooks, os.Getenv("THRONE_TACHYON_PATH"), []byte(in.GetConfig()))
		if err != nil {
			return nil, err
		}
		defer instance.Close()
	}
	if instance == nil {
		return nil, errInstanceNotRunning
	}
	tags := in.OutboundTags
	if in.GetTestCurrent() {
		if tag := instance.ProbeTag(); tag != "" {
			tags = []string{tag}
		} else {
			return nil, errors.New("Tachyon URL test has no default outbound")
		}
	}
	if in.GetUseDefaultOutbound() && !in.GetTestCurrent() {
		var config struct {
			Route struct {
				Final string `json:"final"`
			} `json:"route"`
			Outbounds []struct {
				Tag string `json:"tag"`
			} `json:"outbounds"`
		}
		if err := json.Unmarshal([]byte(in.GetConfig()), &config); err != nil {
			return nil, err
		}
		tag := config.Route.Final
		if tag == "" && len(config.Outbounds) > 0 {
			tag = config.Outbounds[0].Tag
		}
		if tag == "" {
			return nil, errors.New("Tachyon URL test has no default outbound")
		}
		tags = []string{tag}
	}
	response := &gen.TestResp{}
	var reported []*probe.URLTestResult
	defer func() { probe.URLReporter.Reclaim(reported) }()
	for _, tag := range tags {
		delay, err := instance.Client().Delay(ctx, tag, in.GetUrl(), in.GetTestTimeoutMs())
		result := &gen.URLTestResp{OutboundTag: To(tag), LatencyMs: To(delay)}
		if err != nil {
			result.Error = To(err.Error())
		}
		response.Results = append(response.Results, result)
		measurement := &probe.URLTestResult{Tag: tag, Duration: time.Duration(delay) * time.Millisecond, Error: err}
		probe.URLReporter.AddResult(measurement)
		reported = append(reported, measurement)
		if ctx.Err() != nil {
			break
		}
	}
	return response, nil
}

func tachyonStopTest(context.Context, *gen.EmptyReq) (*gen.EmptyResp, error) {
	tachyonTests.Lock()
	defer tachyonTests.Unlock()
	if tachyonTests.cancel != nil {
		tachyonTests.cancel()
	}
	return &gen.EmptyResp{}, nil
}

func tachyonSpeedTest(ctx context.Context, in *gen.SpeedTestRequest) (*gen.SpeedTestResponse, error) {
	if !in.GetTestDownload() && !in.GetTestUpload() && !in.GetSimpleDownload() && !in.GetOnlyCountry() {
		return nil, errors.New("cannot run empty test")
	}
	if in.GetNeedXray() || len(in.GetXrayFullConfigs()) > 0 {
		return nil, errors.New("Tachyon speed tests do not support Xray sidecars")
	}
	tachyonTests.Lock()
	if tachyonTests.cancel != nil {
		tachyonTests.Unlock()
		return nil, errors.New("Tachyon test already running")
	}
	ctx, cancel := context.WithCancel(ctx)
	tachyonTests.cancel = cancel
	tachyonTests.Unlock()
	defer func() { cancel(); tachyonTests.Lock(); tachyonTests.cancel = nil; tachyonTests.Unlock() }()
	instance := currentTachyon()
	if !in.GetTestCurrent() {
		var err error
		instance, err = tachyon.Start(ctx, tachyonHooks, os.Getenv("THRONE_TACHYON_PATH"), []byte(in.GetConfig()))
		if err != nil {
			return nil, err
		}
		defer instance.Close()
	}
	if instance == nil {
		return nil, errInstanceNotRunning
	}
	tags := in.OutboundTags
	if in.GetTestCurrent() {
		if tag := instance.ProbeTag(); tag != "" {
			tags = []string{tag}
		} else {
			return nil, errors.New("Tachyon speed test has no default outbound")
		}
	} else if in.GetUseDefaultOutbound() {
		if tag := tachyon.DefaultTag([]byte(in.GetConfig())); tag != "" {
			tags = []string{tag}
		} else {
			return nil, errors.New("Tachyon speed test has no default outbound")
		}
	}
	response := &gen.SpeedTestResponse{}
	var reported []*probe.SpeedTestResult
	defer func() { probe.CountryResults.Reclaim(reported) }()
	for _, tag := range tags {
		dialer, err := instance.Dialer(tag)
		if err != nil {
			response.Results = append(response.Results, speedTestResultToProto(probe.SpeedTestResult{Tag: tag, Error: err}))
			continue
		}
		result := probe.SpeedTestWithDialer(ctx, dialer, tag, in.GetTestDownload(), in.GetTestUpload(),
			in.GetSimpleDownload(), in.GetSimpleDownloadAddr(), time.Duration(in.GetTimeoutMs())*time.Millisecond, in.GetOnlyCountry())
		response.Results = append(response.Results, speedTestResultToProto(*result))
		reported = append(reported, result)
		if ctx.Err() != nil {
			break
		}
	}
	return response, nil
}

var tachyonHandlers = map[string]handlerFn{
	"Start":            handle(tachyonStart),
	"Stop":             handle(tachyonStop),
	"CheckConfig":      handle(tachyonCheck),
	"Test":             handle(tachyonTest),
	"SpeedTest":        handle(tachyonSpeedTest),
	"QuerySpeedTest":   handle(globalServer.QuerySpeedTest),
	"QueryCountryTest": handle(globalServer.QueryCountryTest),
	"StopTest":         handle(tachyonStopTest),
	"QueryConnections": handle(tachyonConnections),
	"CloseConnections": handle(tachyonCloseConnections),
	// v0.1.1 exposes process-wide totals, not the lossless per-outbound counters
	// Throne persists. Do not invent profile statistics from incomplete snapshots.
	"QueryStats":   handle(func(context.Context, *gen.EmptyReq) (*gen.QueryStatsResp, error) { return &gen.QueryStatsResp{}, nil }),
	"QueryURLTest": handle(globalServer.QueryURLTest),
	"QueryAutoSelectors": handle(func(context.Context, *gen.EmptyReq) (*gen.QueryAutoSelectorsResponse, error) {
		return &gen.QueryAutoSelectorsResponse{}, nil
	}),
	"QueryVPNStatus": handle(func(_ context.Context, in *gen.VPNStatusRequest) (*gen.VPNStatusResponse, error) {
		if len(in.EndpointTags) > 0 {
			return nil, errors.New("Tachyon VPN status is unavailable; select built-in sing-box")
		}
		return &gen.VPNStatusResponse{}, nil
	}),
	"IsPrivileged": handle(func(context.Context, *gen.EmptyReq) (*gen.IsPrivilegedResponse, error) {
		return &gen.IsPrivilegedResponse{HasPrivilege: To(false)}, nil
	}),
}

func tachyonDispatch(method string, payload []byte) ([]byte, error) {
	if handler, ok := tachyonHandlers[method]; ok {
		return handler(context.Background(), payload)
	}
	// These operations do not instantiate a sing-box data plane.
	switch method {
	case "GetDefaultInterface", "GenWgKeyPair", "GenQuicSignature", "WarpRegister", "ParseRuleSet", "ScanProbe", "QueryScan", "StopScan", "ScanCheckNetwork":
		return handlers[method](context.Background(), payload)
	default:
		return nil, errors.New("Tachyon backend does not support " + method + "; select built-in sing-box")
	}
}

// Teardown runs when the GUI disconnects, including a force-closed GUI.
func stopTachyonOnDisconnect() {
	if !tachyonSelected() {
		return
	}
	lifecycleMu.Lock()
	tachyonDisconnected = true
	lifecycleMu.Unlock()
	_, _ = tachyonStopTest(context.Background(), &gen.EmptyReq{})
	_, _ = tachyonStop(context.Background(), &gen.EmptyReq{})
}
