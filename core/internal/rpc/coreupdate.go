package rpc

import (
	"context"
	"path/filepath"
	"time"

	"ThroneCore/gen"
	"ThroneCore/internal/coreupdate"
)

func (s *server) InstallTachyon(ctx context.Context, in *gen.InstallTachyonRequest) (*gen.InstallTachyonResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	result, err := (coreupdate.Installer{Adopt: adoptExtracted}).Install(ctx, in.GetArchivePath(), in.GetTargetDir(), in.GetVersion(), in.GetSha256())
	if err != nil {
		return &gen.InstallTachyonResponse{Error: To(err.Error())}, nil
	}
	return &gen.InstallTachyonResponse{Version: To(result.Version), Path: To(filepath.Join(in.GetTargetDir(), result.Path)), Changed: To(result.Changed)}, nil
}
