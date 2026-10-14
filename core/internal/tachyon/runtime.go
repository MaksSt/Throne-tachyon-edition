package tachyon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Hooks reuse Throne's private config files, privilege drop and child supervision.
type Hooks struct {
	Config func(string) (path, cleanup string, err error)
	Remove func(string) error
	Check  func(context.Context, string, []string) error
	Launch func(string, []string, string) (Child, error)
}

type Child interface {
	Stop()
	Done() <-chan struct{}
	Err() error
}

type Runtime struct {
	child    Child
	api      *Client
	probeTag string
	probes   *probeAccess
}

func executable(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("Tachyon executable must have an absolute path")
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("Tachyon executable: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("Tachyon executable is not a regular file")
	}
	return path, nil
}

func checkedConfig(ctx context.Context, hooks Hooks, path string, raw []byte) (string, string, *Client, error) {
	path, err := executable(path)
	if err != nil {
		return "", "", nil, err
	}
	address, secret, err := controllerCredentials()
	if err != nil {
		return "", "", nil, err
	}
	prepared, err := Prepare(raw, address, secret)
	if err != nil {
		return "", "", nil, err
	}
	configPath, cleanup, err := hooks.Config(string(prepared))
	if err != nil {
		return "", "", nil, err
	}
	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err = hooks.Check(checkCtx, path, []string{"check", "-c", configPath}); err != nil {
		_ = hooks.Remove(cleanup)
		return "", "", nil, fmt.Errorf("tachyon-core check failed (see core log): %w", err)
	}
	return configPath, cleanup, NewClient("http://"+address, secret), nil
}

func Check(ctx context.Context, hooks Hooks, path string, raw []byte) error {
	_, cleanup, client, err := checkedConfig(ctx, hooks, path, raw)
	if err == nil {
		client.Close()
		return hooks.Remove(cleanup)
	}
	return err
}

func Start(ctx context.Context, hooks Hooks, path string, raw []byte) (*Runtime, error) {
	probeConfig, probes, err := prepareProbes(raw)
	if err != nil {
		return nil, err
	}
	configPath, cleanup, client, err := checkedConfig(ctx, hooks, path, probeConfig)
	if err != nil {
		return nil, err
	}
	child, err := hooks.Launch(path, []string{"run", "-c", configPath}, cleanup)
	if err != nil {
		client.Close()
		_ = hooks.Remove(cleanup)
		return nil, err
	}
	r := &Runtime{child: child, api: client, probeTag: ProbeTag(raw), probes: probes}
	readyCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		var version struct {
			Version string `json:"version"`
		}
		if err = client.Request(readyCtx, http.MethodGet, "/version", &version); err == nil && version.Version != "" {
			select {
			case <-child.Done():
				r.Close()
				return nil, fmt.Errorf("Tachyon exited during startup: %v", child.Err())
			default:
				return r, nil
			}
		}
		select {
		case <-child.Done():
			r.Close()
			return nil, fmt.Errorf("Tachyon exited during startup: %v", child.Err())
		case <-readyCtx.Done():
			r.Close()
			return nil, fmt.Errorf("Tachyon control API did not become ready: %w", readyCtx.Err())
		case <-ticker.C:
		}
	}
}

func (r *Runtime) Close()           { r.child.Stop(); r.api.Close() }
func (r *Runtime) Client() *Client  { return r.api }
func (r *Runtime) ProbeTag() string { return r.probeTag }
