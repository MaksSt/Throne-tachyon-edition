package tachyon

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

type testChild struct {
	done chan struct{}
	once sync.Once
	stop func()
}

func (p *testChild) Stop() {
	p.once.Do(func() {
		if p.stop != nil {
			p.stop()
		}
		close(p.done)
	})
}
func (p *testChild) Done() <-chan struct{} { return p.done }
func (p *testChild) Err() error            { return errors.New("exit 1") }

func testHooks(t *testing.T) (Hooks, string, *string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "tachyon")
	if err := os.WriteFile(path, []byte("test executable"), 0o700); err != nil {
		t.Fatal(err)
	}
	var configPath string
	hooks := Hooks{
		Config: func(raw string) (string, string, error) {
			file, err := os.CreateTemp(dir, "private-*.json")
			if err != nil {
				return "", "", err
			}
			configPath = file.Name()
			_, err = file.WriteString(raw)
			closeErr := file.Close()
			return configPath, configPath, errors.Join(err, closeErr)
		},
		Remove: os.Remove,
		Check:  func(context.Context, string, []string) error { return nil },
	}
	return hooks, path, &configPath
}

func TestCheckFailureRemovesPrivateConfigAndNeverLaunches(t *testing.T) {
	hooks, path, configPath := testHooks(t)
	hooks.Check = func(context.Context, string, []string) error { return errors.New("exit 1") }
	hooks.Launch = func(string, []string, string) (Child, error) { t.Fatal("launched after failed check"); return nil, nil }
	if _, err := Start(context.Background(), hooks, path, []byte(`{}`)); err == nil {
		t.Fatal("failed check accepted")
	}
	if _, err := os.Stat(*configPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("private config leaked")
	}
}

func TestStartWaitsForAuthenticatedAPIAndStopsChild(t *testing.T) {
	hooks, path, configPath := testHooks(t)
	var child *testChild
	hooks.Launch = func(path string, args []string, cleanup string) (Child, error) {
		raw, err := os.ReadFile(args[2])
		if err != nil {
			return nil, err
		}
		var config struct {
			Experimental struct {
				API struct {
					Address string `json:"external_controller"`
					Secret  string `json:"secret"`
				} `json:"clash_api"`
			} `json:"experimental"`
		}
		if err = json.Unmarshal(raw, &config); err != nil {
			return nil, err
		}
		api := config.Experimental.API
		listener, err := net.Listen("tcp", api.Address)
		if err != nil {
			return nil, err
		}
		server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer "+api.Secret {
				w.WriteHeader(401)
				return
			}
			_, _ = w.Write([]byte(`{"version":"0.1.1"}`))
		})}
		go func() { _ = server.Serve(listener) }()
		child = &testChild{done: make(chan struct{}), stop: func() { _ = server.Close(); _ = hooks.Remove(cleanup) }}
		return child, nil
	}
	instance, err := Start(context.Background(), hooks, path, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	instance.Close()
	select {
	case <-child.Done():
	default:
		t.Fatal("child not stopped")
	}
	if _, err = os.Stat(*configPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("private config leaked")
	}
}

func TestLaunchFailureAndEarlyExitCleanUp(t *testing.T) {
	for _, earlyExit := range []bool{false, true} {
		t.Run(map[bool]string{false: "launch failure", true: "early exit"}[earlyExit], func(t *testing.T) {
			hooks, path, configPath := testHooks(t)
			hooks.Launch = func(string, []string, string) (Child, error) {
				if !earlyExit {
					return nil, errors.New("launch failed")
				}
				child := &testChild{done: make(chan struct{}), stop: func() { _ = hooks.Remove(*configPath) }}
				child.Stop()
				return child, nil
			}
			if _, err := Start(context.Background(), hooks, path, []byte(`{}`)); err == nil {
				t.Fatal("startup failure accepted")
			}
			if _, err := os.Stat(*configPath); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("private config leaked")
			}
		})
	}
}

func TestCancelledStartupStopsChild(t *testing.T) {
	hooks, path, configPath := testHooks(t)
	var child *testChild
	hooks.Launch = func(string, []string, string) (Child, error) {
		child = &testChild{done: make(chan struct{}), stop: func() { _ = hooks.Remove(*configPath) }}
		return child, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Start(ctx, hooks, path, []byte(`{}`)); err == nil {
		t.Fatal("cancel ignored")
	}
	select {
	case <-child.Done():
	default:
		t.Fatal("cancelled child not stopped")
	}
}

func TestMissingExecutableNeverLaunchesAnotherCore(t *testing.T) {
	dir := t.TempDir()
	for _, path := range []string{"relative-core", filepath.Join(dir, "missing-core"), dir} {
		hooks := Hooks{
			Config: func(string) (string, string, error) {
				t.Fatal("created config for invalid executable")
				return "", "", nil
			},
			Check:  func(context.Context, string, []string) error { t.Fatal("checked another executable"); return nil },
			Launch: func(string, []string, string) (Child, error) { t.Fatal("launched fallback core"); return nil, nil },
		}
		if _, err := Start(context.Background(), hooks, path, []byte(`{}`)); err == nil {
			t.Fatalf("invalid executable accepted: %s", path)
		}
	}
}
