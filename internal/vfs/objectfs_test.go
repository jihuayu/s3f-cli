package vfs

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"s3f-cli/internal/model"
	"s3f-cli/internal/store"
)

type mapStore struct {
	objects    map[string]map[string]store.ObjectInfo
	bodies     map[string][]byte
	failDelete map[string]bool
}

func newMapStore() *mapStore {
	return &mapStore{
		objects:    map[string]map[string]store.ObjectInfo{},
		bodies:     map[string][]byte{},
		failDelete: map[string]bool{},
	}
}

func (m *mapStore) bucket(bucket string) map[string]store.ObjectInfo {
	if _, ok := m.objects[bucket]; !ok {
		m.objects[bucket] = map[string]store.ObjectInfo{}
	}
	return m.objects[bucket]
}

func (m *mapStore) bodyKey(bucket, key string) string {
	return bucket + ":" + key
}

func (m *mapStore) ListPrefix(_ context.Context, bucket, prefix, delimiter, token string, limit int32) (store.ListResult, error) {
	if token != "" {
		return store.ListResult{}, nil
	}

	var keys []string
	for key := range m.bucket(bucket) {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)

	result := store.ListResult{}
	seenDirs := map[string]bool{}
	for _, key := range keys {
		info := m.bucket(bucket)[key]
		rest := strings.TrimPrefix(key, prefix)
		if delimiter == "/" {
			if idx := strings.Index(rest, "/"); idx >= 0 && rest != "" && idx < len(rest)-1 {
				dir := prefix + rest[:idx+1]
				if !seenDirs[dir] {
					result.CommonDirs = append(result.CommonDirs, dir)
					seenDirs[dir] = true
				}
				continue
			}
		}
		result.Objects = append(result.Objects, info)
	}

	if limit > 0 && int(limit) < len(result.Objects) {
		result.Objects = result.Objects[:limit]
		result.Truncated = true
		result.NextToken = "more"
	}
	return result, nil
}

func (m *mapStore) Head(_ context.Context, bucket, key string) (store.ObjectInfo, error) {
	info, ok := m.bucket(bucket)[key]
	if !ok {
		return store.ObjectInfo{}, store.ErrNotFound
	}
	return info, nil
}

func (m *mapStore) Get(_ context.Context, bucket, key string, opts store.GetOptions) (io.ReadCloser, store.ObjectInfo, error) {
	info, ok := m.bucket(bucket)[key]
	if !ok {
		return nil, store.ObjectInfo{}, store.ErrNotFound
	}
	body := m.bodies[m.bodyKey(bucket, key)]
	start := opts.Offset
	if start > int64(len(body)) {
		start = int64(len(body))
	}
	end := int64(len(body))
	if opts.Length > 0 && start+opts.Length < end {
		end = start + opts.Length
	}
	return io.NopCloser(bytes.NewReader(body[start:end])), info, nil
}

func (m *mapStore) Put(_ context.Context, bucket, key string, body io.Reader, _ store.PutOptions) (store.ObjectInfo, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return store.ObjectInfo{}, err
	}
	info := store.ObjectInfo{
		Key:          key,
		Size:         int64(len(data)),
		LastModified: time.Now().UTC(),
		ETag:         "etag-" + key,
		IsMarker:     strings.HasSuffix(key, "/") && len(data) == 0,
	}
	m.bucket(bucket)[key] = info
	m.bodies[m.bodyKey(bucket, key)] = data
	return info, nil
}

func (m *mapStore) Copy(ctx context.Context, srcBucket, srcKey, dstBucket, dstKey string, _ store.CopyOptions) (store.ObjectInfo, error) {
	reader, _, err := m.Get(ctx, srcBucket, srcKey, store.GetOptions{})
	if err != nil {
		return store.ObjectInfo{}, err
	}
	defer reader.Close()
	return m.Put(ctx, dstBucket, dstKey, reader, store.PutOptions{})
}

func (m *mapStore) Delete(_ context.Context, bucket, key string) error {
	if m.failDelete[bucket+":"+key] {
		return io.ErrUnexpectedEOF
	}
	if _, ok := m.bucket(bucket)[key]; !ok {
		return store.ErrNotFound
	}
	delete(m.bucket(bucket), key)
	delete(m.bodies, m.bodyKey(bucket, key))
	return nil
}

func (m *mapStore) MultipartCopy(ctx context.Context, srcBucket, srcKey, dstBucket, dstKey string, _ int64, opts store.CopyOptions) (store.ObjectInfo, error) {
	return m.Copy(ctx, srcBucket, srcKey, dstBucket, dstKey, opts)
}

func (m *mapStore) MultipartUpload(ctx context.Context, bucket, key string, body io.Reader, _ int64, opts store.PutOptions) (store.ObjectInfo, error) {
	return m.Put(ctx, bucket, key, body, opts)
}

func TestObjectVFSStatListAndMkdir(t *testing.T) {
	ctx := context.Background()
	memStore := newMapStore()
	v := NewObjectVFS(memStore)

	if err := v.MakeDirAll(ctx, model.ResolvedPath{Bucket: "bucket-a", Key: "logs/app", IsDirHint: true}); err != nil {
		t.Fatalf("MakeDirAll() error = %v", err)
	}
	if _, err := memStore.Put(ctx, "bucket-a", "logs/app/output.txt", strings.NewReader("hello"), store.PutOptions{}); err != nil {
		t.Fatalf("Put() error = %v", err)
	}

	dirNode, err := v.Stat(ctx, model.ResolvedPath{Bucket: "bucket-a", Key: "logs/app", IsDirHint: true}, StatOptions{AllowMarker: true})
	if err != nil {
		t.Fatalf("Stat(dir) error = %v", err)
	}
	if dirNode.Kind != model.NodeKindDir {
		t.Fatalf("dir kind = %q, want dir", dirNode.Kind)
	}

	fileNode, err := v.Stat(ctx, model.ResolvedPath{Bucket: "bucket-a", Key: "logs/app/output.txt"}, StatOptions{})
	if err != nil {
		t.Fatalf("Stat(file) error = %v", err)
	}
	if fileNode.Kind != model.NodeKindFile {
		t.Fatalf("file kind = %q, want file", fileNode.Kind)
	}

	nodes, err := v.List(ctx, model.ResolvedPath{Bucket: "bucket-a", Key: "logs/app", IsDirHint: true}, ListOptions{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("len(nodes) = %d, want 1", len(nodes))
	}
	if nodes[0].Name != "output.txt" {
		t.Fatalf("listed name = %q, want output.txt", nodes[0].Name)
	}
}

func TestObjectVFSCopyMoveAndFind(t *testing.T) {
	ctx := context.Background()
	memStore := newMapStore()
	v := NewObjectVFS(memStore)

	_, _ = memStore.Put(ctx, "bucket-a", "src/data.txt", strings.NewReader("payload"), store.PutOptions{})
	_, _ = memStore.Put(ctx, "bucket-a", "tree/", bytes.NewReader(nil), store.PutOptions{})
	_, _ = memStore.Put(ctx, "bucket-a", "tree/a.txt", strings.NewReader("a"), store.PutOptions{})
	_, _ = memStore.Put(ctx, "bucket-a", "tree/nested/", bytes.NewReader(nil), store.PutOptions{})
	_, _ = memStore.Put(ctx, "bucket-a", "tree/nested/b.log", strings.NewReader("b"), store.PutOptions{})

	if err := v.Copy(ctx,
		model.ResolvedPath{Bucket: "bucket-a", Key: "src/data.txt"},
		model.ResolvedPath{Bucket: "bucket-a", Key: "dst/data.txt"},
		CopyOptions{MultipartThreshold: 1},
	); err != nil {
		t.Fatalf("Copy(file) error = %v", err)
	}

	if _, err := memStore.Head(ctx, "bucket-a", "dst/data.txt"); err != nil {
		t.Fatalf("copied file missing: %v", err)
	}

	if err := v.Copy(ctx,
		model.ResolvedPath{Bucket: "bucket-a", Key: "tree", IsDirHint: true},
		model.ResolvedPath{Bucket: "bucket-a", Key: "tree-copy", IsDirHint: true},
		CopyOptions{Recursive: true},
	); err != nil {
		t.Fatalf("Copy(tree) error = %v", err)
	}

	if _, err := memStore.Head(ctx, "bucket-a", "tree-copy/nested/b.log"); err != nil {
		t.Fatalf("copied tree object missing: %v", err)
	}

	memStore.failDelete["bucket-a:src/data.txt"] = true
	moveResult, err := v.Move(ctx,
		model.ResolvedPath{Bucket: "bucket-a", Key: "src/data.txt"},
		model.ResolvedPath{Bucket: "bucket-a", Key: "archive/data.txt"},
		MoveOptions{},
	)
	if err != nil {
		t.Fatalf("Move() unexpected error = %v", err)
	}
	if !moveResult.Partial {
		t.Fatalf("Move() partial = false, want true")
	}

	nodes, err := v.Find(ctx, model.ResolvedPath{Bucket: "bucket-a", Key: "tree-copy", IsDirHint: true}, FindOptions{
		MaxDepth:    2,
		NamePattern: "*.log",
		Type:        model.NodeKindFile,
	})
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	if len(nodes) != 1 || !strings.HasSuffix(nodes[0].Path, "/tree-copy/nested/b.log") {
		t.Fatalf("Find() = %#v, want only nested log file", nodes)
	}
}

func TestObjectVFSCopyBetweenLocalAndRemote(t *testing.T) {
	ctx := context.Background()
	memStore := newMapStore()
	v := NewObjectVFS(memStore)

	tmpDir := t.TempDir()
	localPath := filepath.Join(tmpDir, "local.txt")
	if err := os.WriteFile(localPath, []byte("hello local"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if err := v.Copy(ctx,
		model.ResolvedPath{IsLocal: true, Key: localPath},
		model.ResolvedPath{Bucket: "bucket-a", Key: "uploads/local.txt"},
		CopyOptions{},
	); err != nil {
		t.Fatalf("Copy(local->remote) error = %v", err)
	}

	downloadPath := filepath.Join(tmpDir, "download.txt")
	if err := v.Copy(ctx,
		model.ResolvedPath{Bucket: "bucket-a", Key: "uploads/local.txt"},
		model.ResolvedPath{IsLocal: true, Key: downloadPath},
		CopyOptions{},
	); err != nil {
		t.Fatalf("Copy(remote->local) error = %v", err)
	}

	got, err := os.ReadFile(downloadPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(got) != "hello local" {
		t.Fatalf("downloaded body = %q, want hello local", string(got))
	}
}
