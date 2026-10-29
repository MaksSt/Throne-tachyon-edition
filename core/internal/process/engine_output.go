package process

import (
	"fmt"
	"strings"
	"sync"
)

type engineOutput struct {
	mu             sync.Mutex
	tail           string
	startupFailure string
}

func (o *engineOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	o.tail += string(p)
	if len(o.tail) > 8192 {
		o.tail = o.tail[len(o.tail)-8192:]
	}
	if o.startupFailure == "" {
		for _, line := range strings.Split(o.tail, "\n") {
			if strings.Contains(line, "inbound tun[") && strings.Contains(line, "continuing in passive mode") {
				o.startupFailure = strings.TrimSpace(line)
			}
		}
	}
	o.mu.Unlock()
	return (&pipeLogger{prefix: "Tachyon"}).Write(p)
}
func (o *engineOutput) failure() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	lines := strings.Split(o.tail, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, "run:") || strings.HasPrefix(line, "check:") {
			if len(line) > 2048 {
				line = line[:2048]
			}
			return line
		}
	}
	return ""
}

type engineProcess struct {
	Running
	output *engineOutput
}

func (p *engineProcess) StartupError() error {
	p.output.mu.Lock()
	defer p.output.mu.Unlock()
	if p.output.startupFailure != "" {
		return fmt.Errorf("TUN failed to start: %s", p.output.startupFailure)
	}
	return nil
}

func (p *engineProcess) Wait() error {
	err := p.Running.Wait()
	if err != nil {
		if detail := p.output.failure(); detail != "" {
			return fmt.Errorf("%w: %s", err, detail)
		}
	}
	return err
}
