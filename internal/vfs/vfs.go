package vfs

import (
	"context"
	"io"

	"s3f-cli/internal/model"
)

type ByteRange struct {
	Offset int64
	Length int64
}

type StatOptions struct {
	AllowMarker bool
}

type ListOptions struct {
	LongFormat bool
	Recursive  bool
}

type ReadOptions struct {
	Range *ByteRange
}

type CopyOptions struct {
	Recursive          bool
	PreserveMetadata   bool
	MultipartThreshold int64
}

type MoveOptions struct {
	Recursive          bool
	PreserveMetadata   bool
	MultipartThreshold int64
}

type FindOptions struct {
	MaxDepth      int
	NamePattern   string
	Type          model.NodeKind
	WarnThreshold int
}

type MoveResult struct {
	SourceDeleted bool
	Partial       bool
}

type PathResolver interface {
	Resolve(sess *model.Session, input string) (model.ResolvedPath, error)
}

type VFS interface {
	Stat(ctx context.Context, path model.ResolvedPath, opts StatOptions) (model.VNode, error)
	List(ctx context.Context, path model.ResolvedPath, opts ListOptions) ([]model.VNode, error)
	Read(ctx context.Context, path model.ResolvedPath, opts ReadOptions) (io.ReadCloser, error)
	MakeDirAll(ctx context.Context, path model.ResolvedPath) error
	Copy(ctx context.Context, src, dst model.ResolvedPath, opts CopyOptions) error
	Move(ctx context.Context, src, dst model.ResolvedPath, opts MoveOptions) (MoveResult, error)
	Find(ctx context.Context, path model.ResolvedPath, opts FindOptions) ([]model.VNode, error)
}
