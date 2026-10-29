package tachyon

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

// Launch is supplied by Throne's process package so elevated cores use the same
// privilege drop, environment filtering and secure config files as Extra Core.
type Child interface {
	Wait() error
	Kill() error
}
type Launch func(string, []string) (Child, error)
type ConfigFile func(string) (path string, cleanup func(), err error)

func (p *Plan) Check(ctx context.Context, launch Launch, file ConfigFile) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, cleanup, err := file(string(p.Config))
	if err != nil {
		return err
	}
	defer cleanup()
	child, err := launch(p.Path, []string{"check", "-c", path})
	if err != nil {
		return fmt.Errorf("tachyon-core check: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- child.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("tachyon-core rejected the generated config (see core log): %w", err)
		}
		return nil
	case <-ctx.Done():
		_ = child.Kill()
		<-done
		return ctx.Err()
	}
}

type Session struct {
	child   Child
	done    chan struct{}
	err     error
	cleanup func()
	once    sync.Once
}

func (p *Plan) Start(ctx context.Context, launch Launch, file ConfigFile) (*Session, error) {
	if err := p.Check(ctx, launch, file); err != nil {
		return nil, err
	}
	path, cleanup, err := file(string(p.Config))
	if err != nil {
		return nil, err
	}
	child, err := launch(p.Path, []string{"run", "-c", path})
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("tachyon-core run: %w", err)
	}
	s := &Session{child: child, done: make(chan struct{}), cleanup: cleanup}
	go func() { s.err = child.Wait(); close(s.done) }()
	for _, bridge := range p.Bridges {
		for {
			if health, ok := child.(interface{ StartupError() error }); ok {
				if err := health.StartupError(); err != nil {
					s.Stop()
					return nil, err
				}
			}
			select {
			case <-s.done:
				s.Stop()
				return nil, fmt.Errorf("tachyon-core exited before its native listeners were ready: %v", s.err)
			case <-ctx.Done():
				s.Stop()
				return nil, fmt.Errorf("tachyon-core readiness: %w", ctx.Err())
			default:
			}
			if err := authenticate(ctx, bridge); err == nil {
				break
			}
			select {
			case <-ctx.Done():
			case <-s.done:
			case <-time.After(25 * time.Millisecond):
			}
		}
	}
	if health, ok := child.(interface{ StartupError() error }); ok {
		if err := health.StartupError(); err != nil {
			s.Stop()
			return nil, err
		}
	}
	return s, nil
}

func (s *Session) Stop() {
	s.once.Do(func() {
		select {
		case <-s.done:
		default:
			_ = s.child.Kill()
		}
		<-s.done
		s.cleanup()
	})
}

func (s *Session) Done() <-chan struct{} { return s.done }

// Authenticate to our own listener; merely finding an open TCP port could
// accept a different process that won the bind race after port allocation.
func authenticate(ctx context.Context, bridge Bridge) error {
	conn, err := (&net.Dialer{Timeout: 200 * time.Millisecond}).DialContext(ctx, "tcp", bridge.Address)
	if err != nil {
		return err
	}
	defer conn.Close()
	deadline := time.Now().Add(250 * time.Millisecond)
	if end, ok := ctx.Deadline(); ok && end.Before(deadline) {
		deadline = end
	}
	_ = conn.SetDeadline(deadline)
	if _, err = conn.Write([]byte{5, 1, 2}); err != nil {
		return err
	}
	var response [2]byte
	if _, err = io.ReadFull(conn, response[:]); err != nil {
		return err
	}
	if response != [2]byte{5, 2} {
		return fmt.Errorf("SOCKS authentication method rejected")
	}
	request := append([]byte{1, byte(len(bridge.Username))}, bridge.Username...)
	request = append(request, byte(len(bridge.Password)))
	request = append(request, bridge.Password...)
	if _, err = conn.Write(request); err != nil {
		return err
	}
	if _, err = io.ReadFull(conn, response[:]); err != nil {
		return err
	}
	if response != [2]byte{1, 0} {
		return fmt.Errorf("SOCKS credentials rejected")
	}
	return nil
}
