package tachyon

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type integrationChild struct {
	cmd  *exec.Cmd
	done chan struct{}
	err  error
	once sync.Once
}

func (p *integrationChild) Stop()                 { p.once.Do(func() { _ = p.cmd.Process.Kill(); <-p.done }) }
func (p *integrationChild) Done() <-chan struct{} { return p.done }
func (p *integrationChild) Err() error            { return p.err }

// Opt-in real binary test: no TUN, DNS/route changes or OS proxy settings.
func TestRealTachyonProxyLifecycle(t *testing.T) {
	path := os.Getenv("TACHYON_TEST_BINARY")
	if path == "" {
		t.Skip("set TACHYON_TEST_BINARY to test the pinned release")
	}
	path, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	var cleanupPath string
	hooks := Hooks{
		Config: func(raw string) (string, string, error) {
			file, err := os.CreateTemp(dir, "private-*.json")
			if err != nil {
				return "", "", err
			}
			cleanupPath = file.Name()
			_, err = file.WriteString(raw)
			closeErr := file.Close()
			return cleanupPath, cleanupPath, errors.Join(err, closeErr)
		}, Remove: os.Remove,
		Check: func(ctx context.Context, path string, args []string) error {
			return exec.CommandContext(ctx, path, args...).Run()
		},
		Launch: func(path string, args []string, cleanup string) (Child, error) {
			cmd := exec.Command(path, args...)
			if err := cmd.Start(); err != nil {
				return nil, err
			}
			child := &integrationChild{cmd: cmd, done: make(chan struct{})}
			go func() { child.err = cmd.Wait(); _ = os.Remove(cleanup); close(child.done) }()
			return child, nil
		},
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	_, port, _ := net.SplitHostPort(address)
	raw := []byte(`{"log":{"level":"warn"},"inbounds":[{"type":"mixed","tag":"mixed-in","listen":"127.0.0.1","listen_port":` + port + `}],"outbounds":[{"type":"direct","tag":"proxy"}],"route":{"final":"proxy","auto_detect_interface":true,"find_process":true},"experimental":{"cache_file":{"enabled":false}},"services":[{"type":"api","listen":"127.0.0.1","listen_port":0,"secret":"unused"}]}`)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	instance, err := Start(ctx, hooks, path, raw)
	if err != nil {
		t.Fatal(err)
	}
	defer instance.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Tachyon v0.1.1 treats a rounded 0 ms delay as a failed probe.
		if r.Method == http.MethodHead {
			time.Sleep(15 * time.Millisecond)
		}
		_, _ = io.WriteString(w, "tachyon-proxy-payload")
	}))
	defer origin.Close()
	proxyURL, _ := url.Parse("http://" + address)
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL), DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	response, err := client.Get(origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || string(body) != "tachyon-proxy-payload" {
		t.Fatalf("payload=%q err=%v", body, err)
	}
	snapshot, err := instance.Client().Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = instance.Client().Delay(ctx, "proxy", origin.URL, 3000)
	if err != nil {
		t.Fatal(err)
	}
	// Keep a CONNECT tunnel alive, then close its actual Tachyon connection id.
	tunnel, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer tunnel.Close()
	_ = tunnel.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = io.WriteString(tunnel, "CONNECT "+strings.TrimPrefix(origin.URL, "http://")+" HTTP/1.1\r\nHost: localhost\r\n\r\n")
	reader := bufio.NewReader(tunnel)
	connected, err := http.ReadResponse(reader, &http.Request{Method: "CONNECT"})
	if err != nil || connected.StatusCode != 200 {
		t.Fatalf("CONNECT failed: %v %v", connected, err)
	}
	_, _ = io.WriteString(tunnel, "GET / HTTP/1.1\r\nHost: localhost\r\n\r\n")
	reply, err := http.ReadResponse(reader, &http.Request{Method: "GET"})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := io.ReadAll(reply.Body)
	_ = reply.Body.Close()
	if err != nil || string(payload) != "tachyon-proxy-payload" {
		t.Fatalf("tunnel payload=%q err=%v", payload, err)
	}
	snapshot, err = instance.Client().Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Connections) == 0 {
		t.Fatal("live connection missing")
	}
	if snapshot.Upload <= 0 || snapshot.Download <= 0 {
		t.Fatalf("no tracked CONNECT payload: %+v", snapshot)
	}
	closed, err := instance.Client().CloseConnection(ctx, snapshot.Connections[0].ID)
	if err != nil || !closed {
		t.Fatal(err)
	}
	closed, err = instance.Client().CloseConnection(ctx, snapshot.Connections[0].ID)
	if err != nil || closed {
		t.Fatalf("stale connection counted as closed: %v %v", closed, err)
	}
	if _, err = reader.ReadByte(); err == nil {
		t.Fatal("API did not close tunnel")
	}
	instance.Close()
	if _, err = os.Stat(cleanupPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("private config leaked after stop")
	}
	if conn, err := net.DialTimeout("tcp", address, time.Second); err == nil {
		_ = conn.Close()
		t.Fatal("proxy listener survived stop")
	}
	// A second start demonstrates the profile can be stopped and started again.
	instance, err = Start(ctx, hooks, path, raw)
	if err != nil {
		t.Fatal(err)
	}
	instance.Close()
}
