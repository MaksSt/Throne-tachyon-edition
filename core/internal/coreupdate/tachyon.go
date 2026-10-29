// Package coreupdate installs verified Tachyon releases in the host's data directory.
package coreupdate

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"ThroneCore/internal/process"
)

const maxArchive = 64 << 20
const maxBinary = 128 << 20

var installMu sync.Mutex
var releaseTag = regexp.MustCompile(`^v?[0-9][0-9A-Za-z._-]{0,79}$`)

type Manifest struct {
	Version       string `json:"version"`
	Path          string `json:"path"`
	ArchiveSHA256 string `json:"archive_sha256"`
	BinarySHA256  string `json:"binary_sha256"`
}

type Result struct {
	Manifest
	Changed bool
}

// Installer accepts an archive already downloaded by Throne's networking layer.
// Validate is injectable for archive/rollback tests; production always launches
// the staged binary's version command before publishing it.
type Installer struct {
	Validate func(context.Context, string) error
	Adopt    func(string) error
}

func BinaryName() string {
	if runtime.GOOS == "windows" {
		return "tachyon-core.exe"
	}
	return "tachyon-core"
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (i Installer) Install(ctx context.Context, archivePath, root, version, expected string) (Result, error) {
	installMu.Lock()
	defer installMu.Unlock()
	var result Result
	if !filepath.IsAbs(root) || !releaseTag.MatchString(version) {
		return result, fmt.Errorf("invalid Tachyon install directory or release tag")
	}
	decoded, err := hex.DecodeString(expected)
	if err != nil || len(decoded) != sha256.Size {
		return result, fmt.Errorf("release has no valid SHA-256 digest")
	}
	expected = strings.ToLower(expected)
	f, err := os.Open(archivePath)
	if err != nil {
		return result, err
	}
	data, err := io.ReadAll(io.LimitReader(f, maxArchive+1))
	_ = f.Close()
	if err != nil {
		return result, err
	}
	if len(data) > maxArchive || digest(data) != expected {
		return result, fmt.Errorf("Tachyon archive SHA-256 mismatch or size limit exceeded")
	}
	binary, err := extractBinary(data)
	if err != nil {
		return result, err
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	if err = os.MkdirAll(root, 0o755); err != nil {
		return result, err
	}
	if info, e := os.Lstat(root); e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return result, fmt.Errorf("Tachyon install directory is not a real directory")
	}
	oldData, _ := os.ReadFile(filepath.Join(root, "current.json"))
	var old Manifest
	if json.Unmarshal(oldData, &old) == nil && old.Version == version && old.ArchiveSHA256 == expected && old.BinarySHA256 == digest(binary) && filepath.IsLocal(old.Path) {
		installed, e := os.ReadFile(filepath.Join(root, old.Path))
		if e == nil && digest(installed) == old.BinarySHA256 {
			return Result{Manifest: old}, nil
		}
	}
	// Each release has its own immutable directory. Running Windows executables
	// remain untouched, and existing connections keep their original process.
	stage, err := os.MkdirTemp(root, "release-"+expected[:12]+"-")
	if err != nil {
		return result, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(stage)
		}
	}()
	if err = os.Chmod(stage, 0o755); err != nil {
		return result, err
	}
	executable := filepath.Join(stage, BinaryName())
	if err = os.WriteFile(executable, binary, 0o755); err != nil {
		return result, err
	}
	validate := i.Validate
	if validate == nil {
		validate = validateReleaseBinary
	}
	if err = validate(ctx, executable); err != nil {
		return result, fmt.Errorf("Tachyon release failed validation: %w", err)
	}
	relative, _ := filepath.Rel(root, executable)
	manifest := Manifest{Version: version, Path: filepath.ToSlash(relative), ArchiveSHA256: expected, BinarySHA256: digest(binary)}
	manifestData, _ := json.Marshal(manifest)
	if i.Adopt != nil {
		if err = i.Adopt(root); err != nil {
			return result, err
		}
	}
	if len(oldData) > 0 {
		if err = atomicWrite(root, "previous.json", oldData); err != nil {
			return result, err
		}
	}
	if err = atomicWrite(root, "current.json", manifestData); err != nil {
		return result, err
	}
	keep = true
	return Result{Manifest: manifest, Changed: true}, nil
}

func atomicWrite(root, name string, data []byte) error {
	f, err := os.CreateTemp(root, ".manifest-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Chmod(f.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(root, name))
}

func validateReleaseBinary(ctx context.Context, executable string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	child, err := process.StartEngine(executable, []string{"version"})
	if err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- child.Wait() }()
	select {
	case err = <-done:
		return err
	case <-ctx.Done():
		_ = child.Kill()
		<-done
		return ctx.Err()
	}
}

func extractBinary(data []byte) ([]byte, error) {
	var binary []byte
	accept := func(name string, regular bool, reader io.Reader) error {
		// Never extract archive paths to disk, and reject traversal and links even
		// when their basename resembles the executable.
		clean := strings.TrimPrefix(name, "./")
		if path.Base(clean) != BinaryName() {
			return nil
		}
		if !regular || strings.Contains(clean, "\\") || !filepath.IsLocal(filepath.FromSlash(clean)) {
			return fmt.Errorf("unsafe Tachyon archive entry")
		}
		if binary != nil {
			return fmt.Errorf("duplicate Tachyon executable in archive")
		}
		var err error
		binary, err = io.ReadAll(io.LimitReader(reader, maxBinary+1))
		if err != nil {
			return err
		}
		if len(binary) == 0 || len(binary) > maxBinary {
			return fmt.Errorf("invalid Tachyon executable size")
		}
		return nil
	}
	if bytes.HasPrefix(data, []byte("PK")) {
		z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, err
		}
		for _, file := range z.File {
			if path.Base(file.Name) != BinaryName() {
				continue
			}
			r, e := file.Open()
			if e != nil {
				return nil, e
			}
			e = accept(file.Name, file.Mode().IsRegular(), r)
			_ = r.Close()
			if e != nil {
				return nil, e
			}
		}
	} else {
		gz, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		defer gz.Close()
		tr := tar.NewReader(io.LimitReader(gz, maxBinary+16<<20))
		for {
			h, e := tr.Next()
			if e == io.EOF {
				break
			}
			if e != nil {
				return nil, e
			}
			if e = accept(h.Name, h.Typeflag == tar.TypeReg, tr); e != nil {
				return nil, e
			}
		}
	}
	if binary == nil {
		return nil, fmt.Errorf("Tachyon executable missing from release archive")
	}
	return binary, nil
}
