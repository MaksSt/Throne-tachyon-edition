package tachyon

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestControlAPIAuthenticationProxyBypassAndSnapshot(t *testing.T) {
	var proxyCalls atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { proxyCalls.Add(1); w.WriteHeader(500) }))
	defer proxy.Close()
	t.Setenv("HTTP_PROXY", proxy.URL)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer private-secret" {
			t.Error("missing authentication")
		}
		if r.URL.Path != "/connections" {
			t.Error("unexpected API")
		}
		_, _ = w.Write([]byte(`{"uploadTotal":10,"downloadTotal":20,"connections":[{"id":"1","upload":3,"download":4,"start":"2026-10-14T01:00:00Z","chains":["proxy"],"metadata":{"destinationPort":"443","sourcePort":"2080","network":"tcp"}}]}`))
	}))
	defer server.Close()
	client := NewClient(server.URL, "private-secret")
	defer client.Close()
	snapshot, err := client.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Upload != 10 || snapshot.Download != 20 || len(snapshot.Connections) != 1 || snapshot.Connections[0].ID != "1" {
		t.Fatalf("bad snapshot: %+v", snapshot)
	}
	if proxyCalls.Load() != 0 {
		t.Fatal("control API used proxy")
	}
}

func TestControlAPIDoesNotFollowRedirectOrExposeBody(t *testing.T) {
	var redirected atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1) }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", target.URL)
		w.WriteHeader(302)
		_, _ = w.Write([]byte("private config payload"))
	}))
	defer server.Close()
	client := NewClient(server.URL, "private-secret")
	defer client.Close()
	err := client.Request(context.Background(), "GET", "/version", nil)
	if err == nil || strings.Contains(err.Error(), "private") || redirected.Load() != 0 {
		t.Fatalf("unsafe redirect/error: %v", err)
	}
}

func TestDelayEscapesTagAndURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/proxies/a%2Fb%3Fc/delay" || r.URL.Query().Get("url") != "http://example.test/?x=1&y=2" || r.URL.Query().Get("timeout") != "3000" {
			t.Errorf("bad URL: %s", r.URL)
		}
		_, _ = w.Write([]byte(`{"delay":42}`))
	}))
	defer server.Close()
	client := NewClient(server.URL, "secret")
	defer client.Close()
	delay, err := client.Delay(context.Background(), "a/b?c", "http://example.test/?x=1&y=2", 0)
	if err != nil || delay != 42 {
		t.Fatalf("delay=%d err=%v", delay, err)
	}
}

func TestClosingStaleConnectionsPreservesCountAndFailures(t *testing.T) {
	for _, test := range []struct {
		status         int
		closed, failed bool
	}{
		{http.StatusNoContent, true, false}, {http.StatusNotFound, false, false},
		{http.StatusUnauthorized, false, true}, {http.StatusInternalServerError, false, true},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodDelete || r.URL.EscapedPath() != "/connections/a%2Fb" {
				t.Errorf("bad close request: %s %s", r.Method, r.URL)
			}
			w.WriteHeader(test.status)
		}))
		client := NewClient(server.URL, "secret")
		closed, err := client.CloseConnection(context.Background(), "a/b")
		client.Close()
		server.Close()
		if closed != test.closed || (err != nil) != test.failed {
			t.Fatalf("HTTP %d: closed=%v err=%v", test.status, closed, err)
		}
	}
}
