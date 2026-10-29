package tachyon

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func fixture(t *testing.T, outbounds []any) []byte {
	t.Helper()
	data, err := json.Marshal(object{ConfigKey: filepath.Join(t.TempDir(), "tachyon-core.exe"),
		"outbounds": outbounds, "route": object{"final": "proxy"},
		"services": []any{object{"type": "api"}}})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func testFile(t *testing.T) ConfigFile {
	t.Helper()
	return func(content string) (string, func(), error) {
		file, err := os.CreateTemp(t.TempDir(), "config-*.json")
		if err != nil {
			return "", func() {}, err
		}
		_, err = file.WriteString(content)
		_ = file.Close()
		return file.Name(), func() { _ = os.Remove(file.Name()) }, err
	}
}

func binaryPath(t *testing.T) string {
	t.Helper()
	path := os.Getenv("TACHYON_BINARY")
	if path == "" {
		t.Skip("set TACHYON_BINARY for release-binary integration tests")
	}
	return path
}

func execLaunch(path string, args []string) (Child, error) {
	cmd := exec.Command(path, args...)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &execChild{cmd}, nil
}

type execChild struct{ cmd *exec.Cmd }

func (c *execChild) Wait() error { return c.cmd.Wait() }
func (c *execChild) Kill() error { return c.cmd.Process.Kill() }

func TestReleaseBinaryRelaysThroughHTTPOutbound(t *testing.T) {
	path := binaryPath(t)
	echo, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		conn, err := echo.Accept()
		if err == nil {
			defer conn.Close()
			_, _ = io.Copy(conn, conn)
		}
	}()
	reached := make(chan struct{}, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "CONNECT" {
			http.Error(w, "CONNECT required", 400)
			return
		}
		remote, err := net.Dial("tcp", r.Host)
		if err != nil {
			http.Error(w, "dial failed", 502)
			return
		}
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			_ = remote.Close()
			return
		}
		defer conn.Close()
		defer remote.Close()
		_, _ = io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n")
		reached <- struct{}{}
		go func() { _, _ = io.Copy(remote, conn) }()
		_, _ = io.Copy(conn, remote)
	}))
	defer upstream.Close()
	port := upstream.Listener.Addr().(*net.TCPAddr).Port
	native, err := PrepareNative(fixture(t, []any{object{"type": "http", "tag": "proxy", "server": "127.0.0.1", "server_port": port}}))
	if err != nil {
		t.Fatal(err)
	}
	plan := native.Plan
	plan.Path = path
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session, err := plan.Start(ctx, execLaunch, testFile(t))
	if err != nil {
		t.Fatal(err)
	}
	defer session.Stop()
	b := plan.Bridges[0]
	conn, err := net.DialTimeout("tcp", b.Address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	_, _ = conn.Write([]byte{5, 1, 2})
	var reply [2]byte
	if _, err = io.ReadFull(conn, reply[:]); err != nil || reply != [2]byte{5, 2} {
		t.Fatal("SOCKS greeting", reply, err)
	}
	auth := append([]byte{1, byte(len(b.Username))}, b.Username...)
	auth = append(auth, byte(len(b.Password)))
	auth = append(auth, b.Password...)
	_, _ = conn.Write(auth)
	if _, err = io.ReadFull(conn, reply[:]); err != nil || reply != [2]byte{1, 0} {
		t.Fatal("SOCKS auth", reply, err)
	}
	request := []byte{5, 1, 0, 1, 127, 0, 0, 1, 0, 0}
	binary.BigEndian.PutUint16(request[8:], uint16(echo.Addr().(*net.TCPAddr).Port))
	_, _ = conn.Write(request)
	var connectReply [10]byte
	if _, err = io.ReadFull(conn, connectReply[:]); err != nil || connectReply[1] != 0 {
		t.Fatal("SOCKS connect", connectReply, err)
	}
	const payload = "Throne -> Tachyon -> HTTP outbound -> echo"
	_, _ = io.WriteString(conn, payload)
	response := make([]byte, len(payload))
	if _, err = io.ReadFull(conn, response); err != nil || string(response) != payload {
		t.Fatalf("relay: %q %v", response, err)
	}
	select {
	case <-reached:
	default:
		t.Fatal("traffic did not reach selected outbound")
	}
	session.Stop()
	session.Stop()
	if conn, err := net.DialTimeout("tcp", b.Address, 100*time.Millisecond); err == nil {
		_ = conn.Close()
		t.Fatal("bridge survived Stop")
	}
}

func TestReleaseBinaryRejectsInvalidConfig(t *testing.T) {
	path := binaryPath(t)
	plan := &Plan{Path: path, Config: []byte(`{"outbounds":[{"type":"not-a-protocol","tag":"proxy"}]}`)}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := plan.Check(ctx, execLaunch, testFile(t)); err == nil {
		t.Fatal("invalid config accepted")
	}
}

func TestReleaseBinaryVLESSConfig(t *testing.T) {
	path := binaryPath(t)
	input := fixture(t, []any{object{"type": "vless", "tag": "proxy", "server": "example.com", "server_port": 443,
		"uuid": "00000000-0000-0000-0000-000000000001", "tls": object{"enabled": true, "server_name": "example.com"}}})
	native, err := PrepareNative(input)
	if err != nil {
		t.Fatal(err)
	}
	plan := native.Plan
	plan.Path = path
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := plan.Check(ctx, execLaunch, testFile(t)); err != nil {
		t.Fatal(err)
	}
}

func TestReadinessRejectsUnrelatedListener(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			defer conn.Close()
			_, _ = conn.Write([]byte{5, 0})
		}
	}()
	if err := authenticate(context.Background(), Bridge{Address: listener.Addr().String(), Username: "user", Password: "password"}); err == nil {
		t.Fatal("unrelated SOCKS server accepted")
	}
}

type waitingChild struct {
	done chan struct{}
	once sync.Once
}

func (c *waitingChild) Wait() error { <-c.done; return nil }
func (c *waitingChild) Kill() error { c.once.Do(func() { close(c.done) }); return nil }

func TestStartTimeoutReapsChildAndRemovesConfigs(t *testing.T) {
	var run *waitingChild
	created, removed := 0, 0
	file := func(string) (string, func(), error) {
		created++
		return "test-config", func() { removed++ }, nil
	}
	launch := func(_ string, args []string) (Child, error) {
		child := &waitingChild{done: make(chan struct{})}
		if args[0] == "check" {
			_ = child.Kill()
		} else {
			run = child
		}
		return child, nil
	}
	plan := &Plan{Path: "test", Config: []byte(`{}`), Bridges: []Bridge{{Address: "127.0.0.1:0", Username: "user", Password: "password"}}}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if session, err := plan.Start(ctx, launch, file); err == nil || session != nil {
		t.Fatal("unready core accepted")
	}
	if created != 2 || removed != 2 {
		t.Fatalf("config leak: created %d removed %d", created, removed)
	}
	if run == nil {
		t.Fatal("run not started")
	}
	select {
	case <-run.done:
	default:
		t.Fatal("timed-out child was not killed")
	}
}
