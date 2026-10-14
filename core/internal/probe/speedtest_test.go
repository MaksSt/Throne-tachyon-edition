package probe

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSimpleDownloadReportsHTTPAndBrokenBodyButKeepsTimedSamples(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/forbidden":
			w.WriteHeader(403)
		case "/broken":
			w.Header().Set("Content-Length", "1000")
			_, _ = io.WriteString(w, "short")
		case "/timed":
			_, _ = io.WriteString(w, strings.Repeat("sample", 1000))
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		}
	}))
	defer server.Close()
	for _, test := range []struct {
		path   string
		bytes  int64
		failed bool
	}{
		{"/forbidden", 0, true}, {"/broken", 5, true}, {"/timed", 6000, false},
	} {
		res := SpeedTestWithDialer(context.Background(), (&net.Dialer{}).DialContext, "proxy", false, false, true,
			server.URL+test.path, 100*time.Millisecond, false)
		if (res.Error != nil) != test.failed || res.DlBytes != test.bytes {
			t.Fatalf("%s: bytes=%d err=%v", test.path, res.DlBytes, res.Error)
		}
	}
}

func TestCloudflareLargeDownloadUsesSiteReferer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/__down" || r.Header.Get("Referer") != "https://speed.cloudflare.com/" {
			w.WriteHeader(403)
			return
		}
		_, _ = io.WriteString(w, "actual-download")
	}))
	defer server.Close()
	dial := func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "speed.cloudflare.com:80" {
			t.Errorf("unexpected target: %s", address)
		}
		return (&net.Dialer{}).DialContext(ctx, network, strings.TrimPrefix(server.URL, "http://"))
	}
	res := SpeedTestWithDialer(context.Background(), dial, "proxy", false, false, true,
		"http://speed.cloudflare.com/__down?bytes=100000000", time.Second, false)
	if res.Error != nil || res.DlBytes != 15 {
		t.Fatalf("download failed: %v", res)
	}
}
