package store

import (
	"context"
	"io"
	"time"
)

type ObjectInfo struct {
	Key          string
	Size         int64
	LastModified time.Time
	StorageClass string
	ETag         string
	VersionID    string
	IsMarker     bool
}

type ListResult struct {
	Objects     []ObjectInfo
	CommonDirs  []string
	NextToken   string
	Truncated   bool
	RequestCost int
}

type GetOptions struct {
	Offset int64
	Length int64
}

type PutOptions struct {
	ContentType     string
	Metadata        map[string]string
	ExpectedETag    string
	ExpectedVersion string
	StorageClass    string
}

type CopyOptions struct {
	Metadata         map[string]string
	PreserveMetadata bool
	ExpectedETag     string
	ExpectedVersion  string
}

type ObjectStore interface {
	ListPrefix(ctx context.Context, bucket, prefix, delimiter, token string, limit int32) (ListResult, error)
	Head(ctx context.Context, bucket, key string) (ObjectInfo, error)
	Get(ctx context.Context, bucket, key string, opts GetOptions) (io.ReadCloser, ObjectInfo, error)
	Put(ctx context.Context, bucket, key string, body io.Reader, opts PutOptions) (ObjectInfo, error)
	Copy(ctx context.Context, srcBucket, srcKey, dstBucket, dstKey string, opts CopyOptions) (ObjectInfo, error)
	Delete(ctx context.Context, bucket, key string) error
	MultipartCopy(ctx context.Context, srcBucket, srcKey, dstBucket, dstKey string, partSize int64, opts CopyOptions) (ObjectInfo, error)
	MultipartUpload(ctx context.Context, bucket, key string, body io.Reader, partSize int64, opts PutOptions) (ObjectInfo, error)
}
