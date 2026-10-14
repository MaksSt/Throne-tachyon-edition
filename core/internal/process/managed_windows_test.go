//go:build windows

package process

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestManagedShortLivedCommands(t *testing.T) {
	path := filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for i := range 100 {
		if err := RunCommand(ctx, path, []string{"/d", "/c", "exit", "0"}); err != nil {
			t.Fatalf("short-lived child %d: %v", i, err)
		}
	}
}

func TestManagedTachyonChecks(t *testing.T) {
	path := os.Getenv("TACHYON_TEST_BINARY")
	if path == "" {
		t.Skip("set TACHYON_TEST_BINARY to test the pinned release")
	}
	config := filepath.Join(t.TempDir(), "check.json")
	if err := os.WriteFile(config, []byte(`{"outbounds":[{"type":"direct","tag":"proxy"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for i := range 100 {
		if err := RunCommand(ctx, path, []string{"check", "-c", config}); err != nil {
			t.Fatalf("Tachyon check %d: %v", i, err)
		}
	}
	if err := os.WriteFile(config, []byte(`{"outbounds":[{"type":"nonexistent"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	for i := range 100 {
		err := RunCommand(ctx, path, []string{"check", "-c", config})
		if err == nil || strings.Contains(err.Error(), "cannot supervise") {
			t.Fatalf("invalid check %d: %v", i, err)
		}
	}
}

func TestManagedChildThatExitsBeforeJobAssignment(t *testing.T) {
	path := filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
	run, err := startChild(path, []string{"/d", "/c", "exit", "0"}, true)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate the launcher being descheduled after CreateProcess returns.
	time.Sleep(100 * time.Millisecond)
	managed, err := attachManagedJob(run)
	if err != nil {
		t.Fatal(err)
	}
	if err := managed.Wait(); err != nil {
		t.Fatal(err)
	}
}
