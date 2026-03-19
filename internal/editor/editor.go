package editor

import (
	"context"

	"s3f-cli/internal/model"
)

type Session struct {
	Remote    model.ResolvedPath
	TempFile  string
	ETag      string
	VersionID string
}

type Editor interface {
	Edit(ctx context.Context, session Session, force bool) error
}
