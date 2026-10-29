// Package tachyon adapts Throne configuration and controls the native Tachyon runtime.
package tachyon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const ConfigKey = "_throne_tachyon"

type object = map[string]any
type Bridge struct{ Address, Username, Password string }
type Plan struct {
	Path    string
	Config  []byte
	Bridges []Bridge
}

func validateExecutable(path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("tachyon-core: select an absolute executable path in Basic Settings")
	}
	if strings.EqualFold(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)), "ThroneCore") {
		return fmt.Errorf("tachyon-core: ThroneCore is the controller; select the official tachyon-core executable")
	}
	if self, err := os.Executable(); err == nil {
		selected, e1 := os.Stat(path)
		current, e2 := os.Stat(self)
		if e1 == nil && e2 == nil && os.SameFile(selected, current) {
			return fmt.Errorf("tachyon-core: the controller cannot launch itself")
		}
	}
	return nil
}

func resolverFields(value any) (server, strategy string, err error) {
	if value == nil {
		return "", "", nil
	}
	if text, ok := value.(string); ok {
		return text, "", nil
	}
	options, ok := value.(object)
	if !ok {
		return "", "", fmt.Errorf("tachyon-core: invalid domain resolver")
	}
	for key := range options {
		if key != "server" && key != "strategy" {
			return "", "", fmt.Errorf("tachyon-core: resolver option %s is not supported", key)
		}
	}
	server, _ = options["server"].(string)
	strategy, _ = options["strategy"].(string)
	return server, strategy, nil
}
