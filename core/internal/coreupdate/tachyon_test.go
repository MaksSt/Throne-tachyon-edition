package coreupdate

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func archiveFixture(t *testing.T, names []string, content []byte, symlink bool) (string, string) {
	t.Helper()
	var data bytes.Buffer
	z := zip.NewWriter(&data)
	for _, name := range names {
		h := &zip.FileHeader{Name: name}
		h.SetMode(0o755)
		if symlink {
			h.SetMode(os.ModeSymlink | 0o755)
		}
		w, err := z.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "release.zip")
	if err := os.WriteFile(p, data.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return p, digest(data.Bytes())
}

func readManifest(t *testing.T, root, name string) Manifest {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		t.Fatal(err)
	}
	var m Manifest
	if err = json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestInstallUpdatePreservesRunningReleaseAndRollback(t *testing.T) {
	root := filepath.Join(t.TempDir(), "tachyon")
	calls := 0
	installer := Installer{Validate: func(context.Context, string) error { calls++; return nil }}
	archive, sha := archiveFixture(t, []string{"release/" + BinaryName()}, []byte("first executable"), false)
	first, err := installer.Install(context.Background(), archive, root, "v1.0.0", sha)
	if err != nil || !first.Changed {
		t.Fatal(first, err)
	}
	current := readManifest(t, root, "current.json")
	again, err := installer.Install(context.Background(), archive, root, "v1.0.0", sha)
	if err != nil || again.Changed || calls != 1 {
		t.Fatal("same release reinstalled", again, err, calls)
	}
	// Keep the old executable open, as Windows does for a running native engine.
	old, err := os.Open(filepath.Join(root, current.Path))
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	archive, sha = archiveFixture(t, []string{BinaryName()}, []byte("second executable"), false)
	second, err := installer.Install(context.Background(), archive, root, "v1.1.0", sha)
	if err != nil || !second.Changed || first.Path == second.Path {
		t.Fatal(second, err)
	}
	if previous := readManifest(t, root, "previous.json"); previous != current {
		t.Fatal("rollback manifest lost")
	}
	if data, _ := os.ReadFile(filepath.Join(root, first.Path)); string(data) != "first executable" {
		t.Fatal("running executable changed")
	}
	if data, _ := os.ReadFile(filepath.Join(root, second.Path)); string(data) != "second executable" {
		t.Fatal("new executable missing")
	}
}

func TestFailureKeepsInstalledRelease(t *testing.T) {
	root := filepath.Join(t.TempDir(), "tachyon")
	installer := Installer{Validate: func(context.Context, string) error { return nil }}
	archive, sha := archiveFixture(t, []string{BinaryName()}, []byte("good"), false)
	if _, err := installer.Install(context.Background(), archive, root, "v1.0.0", sha); err != nil {
		t.Fatal(err)
	}
	before := readManifest(t, root, "current.json")
	if _, err := installer.Install(context.Background(), archive, root, "v1.1.0", digest([]byte("wrong"))); err == nil {
		t.Fatal("accepted corrupt download")
	}
	installer.Validate = func(context.Context, string) error { return errors.New("bad executable") }
	if _, err := installer.Install(context.Background(), archive, root, "v1.1.0", sha); err == nil {
		t.Fatal("accepted broken executable")
	}
	installer.Validate = func(context.Context, string) error { return nil }
	installer.Adopt = func(string) error { return errors.New("ownership failed") }
	if _, err := installer.Install(context.Background(), archive, root, "v1.1.0", sha); err == nil {
		t.Fatal("published release before ownership completed")
	}
	if after := readManifest(t, root, "current.json"); before != after {
		t.Fatal("failed update changed current release")
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 2 {
		t.Fatal("failed update left staging files", len(entries))
	}
}

func TestRejectUnsafeOrMissingExecutable(t *testing.T) {
	for _, tc := range []struct {
		name  string
		names []string
		link  bool
	}{
		{"missing", []string{"README"}, false},
		{"traversal", []string{"../" + BinaryName()}, false},
		{"duplicate", []string{BinaryName(), "nested/" + BinaryName()}, false},
		{"symlink", []string{BinaryName()}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			archive, sha := archiveFixture(t, tc.names, []byte("binary"), tc.link)
			installer := Installer{Validate: func(context.Context, string) error { t.Fatal("unsafe archive reached validation"); return nil }}
			if _, err := installer.Install(context.Background(), archive, t.TempDir(), "v1.0.0", sha); err == nil {
				t.Fatal("unsafe archive accepted")
			}
		})
	}
}

func TestTarGzipRelease(t *testing.T) {
	var data bytes.Buffer
	gz := gzip.NewWriter(&data)
	tr := tar.NewWriter(gz)
	content := []byte("native executable")
	if err := tr.WriteHeader(&tar.Header{Name: "./" + BinaryName(), Mode: 0o755, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	_, _ = tr.Write(content)
	_ = tr.Close()
	_ = gz.Close()
	binary, err := extractBinary(data.Bytes())
	if err != nil || !bytes.Equal(binary, content) {
		t.Fatal(err)
	}
}

func TestCancelledInstallDoesNotPublish(t *testing.T) {
	archive, sha := archiveFixture(t, []string{BinaryName()}, []byte("binary"), false)
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (Installer{}).Install(ctx, archive, root, "v1.0.0", sha); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "current.json")); !os.IsNotExist(err) {
		t.Fatal("cancelled install published a release")
	}
}

func TestReleaseBinaryInstallsAndRuns(t *testing.T) {
	path := os.Getenv("TACHYON_BINARY")
	if path == "" {
		t.Skip("set TACHYON_BINARY to verify the actual release executable")
	}
	binary, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	archive, sha := archiveFixture(t, []string{BinaryName()}, binary, false)
	result, err := (Installer{}).Install(context.Background(), archive, t.TempDir(), "v1.0.0", sha)
	if err != nil || !result.Changed {
		t.Fatal(result, err)
	}
}
