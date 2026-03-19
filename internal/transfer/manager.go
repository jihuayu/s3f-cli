package transfer

import (
	"context"
	"io"

	"s3f-cli/internal/model"
)

type Manager struct {
	MultipartThreshold int64
	Concurrency        int
	PreserveMetadata   bool
}

type RangeCache interface {
	GetRange(ctx context.Context, path model.ResolvedPath, offset, length int64) ([]byte, bool, error)
	PutRange(ctx context.Context, path model.ResolvedPath, offset int64, data []byte) error
	Invalidate(ctx context.Context, path model.ResolvedPath) error
}

type RemoteWriter interface {
	WriteRemote(ctx context.Context, dst model.ResolvedPath, body io.Reader, size int64) error
}
