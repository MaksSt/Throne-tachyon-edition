package rpc

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ThroneCore/gen"
)

func TestRealTachyonSpeedDownloadAndCancellation(t *testing.T) {
	path := os.Getenv("TACHYON_TEST_BINARY")
	if path == "" {
		t.Skip("set TACHYON_TEST_BINARY to test the pinned release")
	}
	path, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("THRONE_TACHYON_PATH", path)
	t.Setenv("THRONE_GUARD", "")
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/forbidden" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if r.URL.Path == "/slow" {
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		_, _ = io.WriteString(w, strings.Repeat("payload", 16384))
	}))
	defer origin.Close()
	raw := `{"log":{"level":"warn"},"outbounds":[{"type":"direct","tag":"probe-a"},{"type":"block","tag":"blocked"}],"route":{"final":"blocked"}}`
	request := &gen.SpeedTestRequest{Config: To(raw), OutboundTags: []string{"probe-a"}, SimpleDownload: To(true), SimpleDownloadAddr: To(origin.URL), TimeoutMs: To(int32(3000))}
	response, err := tachyonSpeedTest(context.Background(), request)
	if err != nil || len(response.GetResults()) != 1 {
		t.Fatalf("speed: %v %v", response, err)
	}
	result := response.Results[0]
	if result.GetError() != "" || result.GetDlBytes() != int64(7*16384) || result.GetDlSpeed() == "" || result.GetOutboundTag() != "probe-a" {
		t.Fatalf("download through selected outbound failed: %v", result)
	}
	if currentBox() != nil || currentTachyon() != nil {
		t.Fatal("temporary speed probe leaked a data plane")
	}
	request.UseDefaultOutbound = To(true)
	response, err = tachyonSpeedTest(context.Background(), request)
	if err != nil || len(response.GetResults()) != 1 || response.Results[0].GetOutboundTag() != "blocked" || response.Results[0].GetError() == "" {
		t.Fatalf("default outbound ignored route.final: %v %v", response, err)
	}
	request.UseDefaultOutbound = To(false)
	request.SimpleDownloadAddr = To(origin.URL + "/forbidden")
	response, err = tachyonSpeedTest(context.Background(), request)
	if err != nil || len(response.GetResults()) != 1 || !strings.Contains(response.Results[0].GetError(), "HTTP 403") || response.Results[0].GetDlBytes() != 0 {
		t.Fatalf("HTTP error was reported as speed: %v %v", response, err)
	}
	request.SimpleDownloadAddr = To(origin.URL + "/slow")
	finished := make(chan error, 1)
	go func() {
		response, err := tachyonSpeedTest(context.Background(), request)
		if err == nil && (len(response.GetResults()) != 1 || !response.Results[0].GetCancelled()) {
			err = io.ErrUnexpectedEOF
		}
		finished <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		status, err := globalServer.QuerySpeedTest(context.Background(), &gen.EmptyReq{})
		if err != nil {
			t.Fatal(err)
		}
		if status.GetIsRunning() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("speed progress did not become active")
		}
		time.Sleep(10 * time.Millisecond)
	}
	_, _ = tachyonStopTest(context.Background(), &gen.EmptyReq{})
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("speed cancellation did not finish")
	}
}
