package vfs

import (
	"bytes"
	"context"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"s3f-cli/internal/model"
	"s3f-cli/internal/store"
)

type ObjectVFS struct {
	Store store.ObjectStore
}

func NewObjectVFS(objectStore store.ObjectStore) *ObjectVFS {
	return &ObjectVFS{Store: objectStore}
}

func (v *ObjectVFS) Stat(ctx context.Context, target model.ResolvedPath, opts StatOptions) (model.VNode, error) {
	if target.IsLocal {
		return statLocal(target)
	}
	if v.Store == nil {
		return model.VNode{}, model.Unsupported("stat", target.RemotePath(), "object store is not configured")
	}
	if target.Bucket == "" {
		return model.VNode{}, model.InvalidPath("stat", target.RemotePath(), "bucket is required")
	}
	if target.Key == "" {
		return newDirNode(target.Bucket, "", path.Base(target.Bucket)), nil
	}

	if !target.IsDirHint {
		info, err := v.Store.Head(ctx, target.Bucket, target.Key)
		if err == nil {
			if info.IsMarker {
				return newDirNode(target.Bucket, target.Key, leafName(target.Key)), nil
			}
			return newFileNode(target.Bucket, info), nil
		}
		if err != nil && !store.IsNotFound(err) {
			return model.VNode{}, err
		}
	}

	dirKey := model.EnsureDirKey(target.Key)
	if dirKey == "" {
		return newDirNode(target.Bucket, "", path.Base(target.Bucket)), nil
	}

	if opts.AllowMarker || target.IsDirHint {
		if info, err := v.Store.Head(ctx, target.Bucket, dirKey); err == nil {
			return newDirNodeFromInfo(target.Bucket, info), nil
		} else if err != nil && !store.IsNotFound(err) {
			return model.VNode{}, err
		}
	}

	list, err := v.Store.ListPrefix(ctx, target.Bucket, dirKey, "/", "", 1)
	if err != nil && !store.IsNotFound(err) {
		return model.VNode{}, err
	}
	if len(list.Objects) > 0 || len(list.CommonDirs) > 0 {
		return newDirNode(target.Bucket, dirKey, leafName(dirKey)), nil
	}

	return model.VNode{}, model.NewError(model.ErrPathNotFound, "stat", target.RemotePath(), "path does not exist", nil)
}

func (v *ObjectVFS) List(ctx context.Context, target model.ResolvedPath, opts ListOptions) ([]model.VNode, error) {
	if target.IsLocal {
		return listLocal(target)
	}
	if v.Store == nil {
		return nil, model.Unsupported("ls", target.RemotePath(), "object store is not configured")
	}

	node, err := v.Stat(ctx, target, StatOptions{AllowMarker: true})
	if err != nil {
		return nil, err
	}
	if node.Kind == model.NodeKindFile {
		return []model.VNode{node}, nil
	}

	prefix := model.EnsureDirKey(target.Key)
	token := ""
	var nodes []model.VNode
	for {
		page, err := v.Store.ListPrefix(ctx, target.Bucket, prefix, "/", token, 1000)
		if err != nil {
			return nil, err
		}

		for _, dir := range page.CommonDirs {
			nodes = append(nodes, newDirNode(target.Bucket, dir, leafName(dir)))
		}
		for _, object := range page.Objects {
			if object.Key == prefix && object.IsMarker {
				continue
			}
			nodes = append(nodes, newFileNode(target.Bucket, object))
		}

		if !page.Truncated || page.NextToken == "" {
			break
		}
		token = page.NextToken
	}

	sortNodes(nodes)
	return nodes, nil
}

func (v *ObjectVFS) Read(ctx context.Context, target model.ResolvedPath, opts ReadOptions) (io.ReadCloser, error) {
	if target.IsLocal {
		return readLocal(target, opts)
	}
	if v.Store == nil {
		return nil, model.Unsupported("cat", target.RemotePath(), "object store is not configured")
	}

	stat, err := v.Stat(ctx, target, StatOptions{})
	if err != nil {
		return nil, err
	}
	if stat.Kind == model.NodeKindDir {
		return nil, model.InvalidPath("cat", target.RemotePath(), "directories cannot be read with cat")
	}

	getOpts := store.GetOptions{}
	if opts.Range != nil {
		getOpts.Offset = opts.Range.Offset
		getOpts.Length = opts.Range.Length
	}
	body, _, err := v.Store.Get(ctx, target.Bucket, target.Key, getOpts)
	if err != nil {
		if store.IsNotFound(err) {
			return nil, model.NewError(model.ErrObjectNotFound, "cat", target.RemotePath(), "object not found", err)
		}
		return nil, err
	}
	return body, nil
}

func (v *ObjectVFS) MakeDirAll(ctx context.Context, target model.ResolvedPath) error {
	if target.IsLocal {
		return os.MkdirAll(target.Key, 0o755)
	}
	if v.Store == nil {
		return model.Unsupported("mkdir", target.RemotePath(), "object store is not configured")
	}
	if target.Bucket == "" {
		return model.InvalidPath("mkdir", target.RemotePath(), "bucket is required")
	}

	dirKey := model.EnsureDirKey(target.Key)
	if dirKey == "" {
		return nil
	}
	parts := strings.Split(strings.Trim(dirKey, "/"), "/")
	current := ""
	for _, part := range parts {
		current += part + "/"
		if _, err := v.Store.Put(ctx, target.Bucket, current, bytes.NewReader(nil), store.PutOptions{}); err != nil {
			return err
		}
	}
	return nil
}

func (v *ObjectVFS) Copy(ctx context.Context, src, dst model.ResolvedPath, opts CopyOptions) error {
	if src.IsLocal || dst.IsLocal {
		return v.copyMixed(ctx, src, dst, opts)
	}
	if v.Store == nil {
		return model.Unsupported("cp", src.RemotePath(), "object store is not configured")
	}

	srcNode, err := v.Stat(ctx, src, StatOptions{AllowMarker: true})
	if err != nil {
		return err
	}
	if srcNode.Kind == model.NodeKindDir {
		if !opts.Recursive {
			return model.Unsupported("cp", src.RemotePath(), "recursive flag is required for directory copies")
		}
		return v.copyTree(ctx, src, dst, opts)
	}

	return v.copyObject(ctx, src, dst, opts)
}

func (v *ObjectVFS) Move(ctx context.Context, src, dst model.ResolvedPath, opts MoveOptions) (MoveResult, error) {
	if src.IsLocal || dst.IsLocal {
		if err := v.Copy(ctx, src, dst, CopyOptions{
			Recursive:          opts.Recursive,
			PreserveMetadata:   opts.PreserveMetadata,
			MultipartThreshold: opts.MultipartThreshold,
		}); err != nil {
			return MoveResult{}, err
		}
		if err := deleteLocalIfNeeded(src); err != nil {
			return MoveResult{Partial: true}, nil
		}
		return MoveResult{SourceDeleted: true}, nil
	}
	if v.Store == nil {
		return MoveResult{}, model.Unsupported("mv", src.RemotePath(), "object store is not configured")
	}

	srcNode, err := v.Stat(ctx, src, StatOptions{AllowMarker: true})
	if err != nil {
		return MoveResult{}, err
	}
	if srcNode.Kind == model.NodeKindDir {
		if !opts.Recursive {
			return MoveResult{}, model.Unsupported("mv", src.RemotePath(), "recursive flag is required for directory moves")
		}
		if err := v.copyTree(ctx, src, dst, CopyOptions{
			Recursive:          true,
			PreserveMetadata:   opts.PreserveMetadata,
			MultipartThreshold: opts.MultipartThreshold,
		}); err != nil {
			return MoveResult{}, err
		}
		if err := v.deleteTree(ctx, src); err != nil {
			return MoveResult{Partial: true}, nil
		}
		return MoveResult{SourceDeleted: true}, nil
	}

	if err := v.copyObject(ctx, src, dst, CopyOptions{
		PreserveMetadata:   opts.PreserveMetadata,
		MultipartThreshold: opts.MultipartThreshold,
	}); err != nil {
		return MoveResult{}, err
	}
	if err := v.Store.Delete(ctx, src.Bucket, src.Key); err != nil {
		return MoveResult{Partial: true}, nil
	}
	return MoveResult{SourceDeleted: true}, nil
}

func (v *ObjectVFS) Find(ctx context.Context, target model.ResolvedPath, opts FindOptions) ([]model.VNode, error) {
	node, err := v.Stat(ctx, target, StatOptions{AllowMarker: true})
	if err != nil {
		return nil, err
	}
	if node.Kind == model.NodeKindFile {
		if matchesFind(node, opts) {
			return []model.VNode{node}, nil
		}
		return nil, nil
	}

	if target.IsLocal {
		return findLocal(ctx, target, opts)
	}

	if v.Store == nil {
		return nil, model.Unsupported("find", target.RemotePath(), "object store is not configured")
	}

	startDepth := strings.Count(strings.Trim(model.EnsureDirKey(target.Key), "/"), "/")
	results := []model.VNode{node}
	if !matchesFind(node, opts) {
		results = nil
	}

	items, err := v.listAll(ctx, target.Bucket, model.EnsureDirKey(target.Key))
	if err != nil {
		return nil, err
	}

	dirSeen := map[string]bool{}
	for _, item := range items {
		if item.IsMarker {
			dirNode := newDirNodeFromInfo(target.Bucket, item)
			depth := strings.Count(strings.Trim(item.Key, "/"), "/") - startDepth
			if depth <= opts.MaxDepth || opts.MaxDepth < 0 {
				if !dirSeen[dirNode.Path] && matchesFind(dirNode, opts) {
					results = append(results, dirNode)
				}
				dirSeen[dirNode.Path] = true
			}
			continue
		}

		fileNode := newFileNode(target.Bucket, item)
		depth := strings.Count(strings.Trim(item.Key, "/"), "/") - startDepth
		if opts.MaxDepth >= 0 && depth > opts.MaxDepth {
			continue
		}
		if matchesFind(fileNode, opts) {
			results = append(results, fileNode)
		}

		dirPrefix := parentDir(item.Key)
		for dirPrefix != model.EnsureDirKey(target.Key) && dirPrefix != "" {
			depth := strings.Count(strings.Trim(dirPrefix, "/"), "/") - startDepth
			if opts.MaxDepth >= 0 && depth > opts.MaxDepth {
				dirPrefix = parentDir(strings.TrimSuffix(dirPrefix, "/"))
				continue
			}
			dirNode := newDirNode(target.Bucket, dirPrefix, leafName(dirPrefix))
			if !dirSeen[dirNode.Path] && matchesFind(dirNode, opts) {
				results = append(results, dirNode)
			}
			dirSeen[dirNode.Path] = true
			dirPrefix = parentDir(strings.TrimSuffix(dirPrefix, "/"))
		}
	}

	deduped := dedupeNodes(results)
	sortNodes(deduped)
	return deduped, nil
}

func (v *ObjectVFS) copyMixed(ctx context.Context, src, dst model.ResolvedPath, opts CopyOptions) error {
	srcNode, err := v.Stat(ctx, src, StatOptions{AllowMarker: true})
	if err != nil {
		return err
	}
	if srcNode.Kind == model.NodeKindDir {
		return model.Unsupported("cp", src.RemotePath(), "local and remote directory copy is not implemented in this scaffold")
	}

	reader, err := v.Read(ctx, src, ReadOptions{})
	if err != nil {
		return err
	}
	defer reader.Close()

	if dst.IsLocal {
		if err := os.MkdirAll(filepath.Dir(dst.Key), 0o755); err != nil {
			return err
		}
		file, err := os.Create(dst.Key)
		if err != nil {
			return err
		}
		defer file.Close()
		_, err = io.Copy(file, reader)
		return err
	}

	if src.IsLocal {
		partThreshold := opts.MultipartThreshold
		if partThreshold > 0 && srcNode.Size >= partThreshold {
			_, err := v.Store.MultipartUpload(ctx, dst.Bucket, dst.Key, reader, max(partThreshold, 5*1024*1024), store.PutOptions{})
			return err
		}
		_, err := v.Store.Put(ctx, dst.Bucket, dst.Key, reader, store.PutOptions{})
		return err
	}

	return nil
}

func (v *ObjectVFS) copyObject(ctx context.Context, src, dst model.ResolvedPath, opts CopyOptions) error {
	info, err := v.Store.Head(ctx, src.Bucket, src.Key)
	if err != nil {
		return err
	}
	threshold := opts.MultipartThreshold
	if threshold <= 0 {
		threshold = 64 * 1024 * 1024
	}
	if info.Size >= threshold {
		_, err = v.Store.MultipartCopy(ctx, src.Bucket, src.Key, dst.Bucket, dst.Key, max(threshold, 5*1024*1024), store.CopyOptions{
			PreserveMetadata: opts.PreserveMetadata,
		})
		return err
	}
	_, err = v.Store.Copy(ctx, src.Bucket, src.Key, dst.Bucket, dst.Key, store.CopyOptions{
		PreserveMetadata: opts.PreserveMetadata,
	})
	return err
}

func (v *ObjectVFS) copyTree(ctx context.Context, src, dst model.ResolvedPath, opts CopyOptions) error {
	srcPrefix := model.EnsureDirKey(src.Key)
	dstPrefix := model.EnsureDirKey(dst.Key)
	if err := v.MakeDirAll(ctx, model.ResolvedPath{Bucket: dst.Bucket, Key: dstPrefix, IsDirHint: true}); err != nil {
		return err
	}

	objects, err := v.listAll(ctx, src.Bucket, srcPrefix)
	if err != nil {
		return err
	}
	slices.SortFunc(objects, func(a, b store.ObjectInfo) int {
		return strings.Compare(a.Key, b.Key)
	})

	for _, object := range objects {
		rel := strings.TrimPrefix(object.Key, srcPrefix)
		targetKey := dstPrefix + rel
		if object.IsMarker {
			_, err := v.Store.Put(ctx, dst.Bucket, targetKey, bytes.NewReader(nil), store.PutOptions{})
			if err != nil {
				return err
			}
			continue
		}
		if err := v.copyObject(ctx,
			model.ResolvedPath{Bucket: src.Bucket, Key: object.Key},
			model.ResolvedPath{Bucket: dst.Bucket, Key: targetKey},
			opts,
		); err != nil {
			return err
		}
	}
	return nil
}

func (v *ObjectVFS) deleteTree(ctx context.Context, src model.ResolvedPath) error {
	objects, err := v.listAll(ctx, src.Bucket, model.EnsureDirKey(src.Key))
	if err != nil {
		return err
	}
	slices.SortFunc(objects, func(a, b store.ObjectInfo) int {
		return strings.Compare(b.Key, a.Key)
	})
	for _, object := range objects {
		if err := v.Store.Delete(ctx, src.Bucket, object.Key); err != nil {
			return err
		}
	}
	return nil
}

func (v *ObjectVFS) listAll(ctx context.Context, bucket, prefix string) ([]store.ObjectInfo, error) {
	if v.Store == nil {
		return nil, model.Unsupported("list", "/"+bucket+"/"+prefix, "object store is not configured")
	}
	token := ""
	var objects []store.ObjectInfo
	for {
		page, err := v.Store.ListPrefix(ctx, bucket, prefix, "", token, 1000)
		if err != nil {
			return nil, err
		}
		objects = append(objects, page.Objects...)
		if !page.Truncated || page.NextToken == "" {
			break
		}
		token = page.NextToken
	}
	return objects, nil
}

func newFileNode(bucket string, info store.ObjectInfo) model.VNode {
	return model.VNode{
		Path:          "/" + bucket + "/" + info.Key,
		Name:          leafName(info.Key),
		Kind:          model.NodeKindFile,
		Size:          info.Size,
		ModTime:       info.LastModified,
		StorageClass:  info.StorageClass,
		ETag:          info.ETag,
		VersionID:     info.VersionID,
		SyntheticMode: "-rw-r--r--",
	}
}

func newDirNodeFromInfo(bucket string, info store.ObjectInfo) model.VNode {
	return model.VNode{
		Path:          "/" + bucket + "/" + model.EnsureDirKey(info.Key),
		Name:          leafName(info.Key),
		Kind:          model.NodeKindDir,
		ModTime:       info.LastModified,
		ETag:          info.ETag,
		VersionID:     info.VersionID,
		SyntheticMode: "drwxr-xr-x",
	}
}

func newDirNode(bucket, key, name string) model.VNode {
	return model.VNode{
		Path:          "/" + bucket + "/" + model.EnsureDirKey(key),
		Name:          name + "/",
		Kind:          model.NodeKindDir,
		ModTime:       time.Time{},
		SyntheticMode: "drwxr-xr-x",
	}
}

func leafName(key string) string {
	trimmed := strings.Trim(key, "/")
	if trimmed == "" {
		return ""
	}
	parts := strings.Split(trimmed, "/")
	return parts[len(parts)-1]
}

func parentDir(key string) string {
	trimmed := strings.TrimSuffix(strings.Trim(key, "/"), "/")
	if trimmed == "" {
		return ""
	}
	idx := strings.LastIndex(trimmed, "/")
	if idx < 0 {
		return ""
	}
	return trimmed[:idx+1]
}

func sortNodes(nodes []model.VNode) {
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].Kind != nodes[j].Kind {
			return nodes[i].Kind == model.NodeKindDir
		}
		return nodes[i].Name < nodes[j].Name
	})
}

func dedupeNodes(nodes []model.VNode) []model.VNode {
	seen := map[string]bool{}
	result := make([]model.VNode, 0, len(nodes))
	for _, node := range nodes {
		if node.Path == "" || seen[node.Path] {
			continue
		}
		seen[node.Path] = true
		result = append(result, node)
	}
	return result
}

func matchesFind(node model.VNode, opts FindOptions) bool {
	if opts.Type != "" && node.Kind != opts.Type {
		return false
	}
	if opts.NamePattern != "" {
		matched, err := path.Match(opts.NamePattern, strings.TrimSuffix(node.Name, "/"))
		if err != nil || !matched {
			return false
		}
	}
	return true
}

func statLocal(target model.ResolvedPath) (model.VNode, error) {
	info, err := os.Stat(target.Key)
	if err != nil {
		if os.IsNotExist(err) {
			return model.VNode{}, model.NewError(model.ErrPathNotFound, "stat", target.Key, "local path does not exist", err)
		}
		return model.VNode{}, err
	}

	kind := model.NodeKindFile
	mode := "-rw-r--r--"
	name := filepath.Base(target.Key)
	if info.IsDir() {
		kind = model.NodeKindDir
		mode = "drwxr-xr-x"
		name += "/"
	}
	return model.VNode{
		Path:          target.Key,
		Name:          name,
		Kind:          kind,
		Size:          info.Size(),
		ModTime:       info.ModTime(),
		SyntheticMode: mode,
	}, nil
}

func listLocal(target model.ResolvedPath) ([]model.VNode, error) {
	node, err := statLocal(target)
	if err != nil {
		return nil, err
	}
	if node.Kind == model.NodeKindFile {
		return []model.VNode{node}, nil
	}

	entries, err := os.ReadDir(target.Key)
	if err != nil {
		return nil, err
	}
	nodes := make([]model.VNode, 0, len(entries))
	for _, entry := range entries {
		fullPath := filepath.Join(target.Key, entry.Name())
		item, err := statLocal(model.ResolvedPath{IsLocal: true, Key: fullPath})
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, item)
	}
	sortNodes(nodes)
	return nodes, nil
}

func readLocal(target model.ResolvedPath, opts ReadOptions) (io.ReadCloser, error) {
	file, err := os.Open(target.Key)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, model.NewError(model.ErrObjectNotFound, "cat", target.Key, "local file does not exist", err)
		}
		return nil, err
	}

	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	if info.IsDir() {
		file.Close()
		return nil, model.InvalidPath("cat", target.Key, "directories cannot be read with cat")
	}

	if opts.Range == nil {
		return file, nil
	}

	reader := io.NewSectionReader(file, opts.Range.Offset, sectionLength(info.Size(), opts.Range))
	data, err := io.ReadAll(reader)
	file.Close()
	if err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func sectionLength(size int64, r *ByteRange) int64 {
	if r == nil {
		return size
	}
	if r.Length > 0 {
		return r.Length
	}
	return size - r.Offset
}

func deleteLocalIfNeeded(target model.ResolvedPath) error {
	if !target.IsLocal {
		return nil
	}
	return os.RemoveAll(target.Key)
}

func findLocal(_ context.Context, target model.ResolvedPath, opts FindOptions) ([]model.VNode, error) {
	var results []model.VNode
	rootNode, err := statLocal(target)
	if err != nil {
		return nil, err
	}
	rootDepth := strings.Count(strings.Trim(filepath.Clean(target.Key), string(os.PathSeparator)), string(os.PathSeparator))
	if matchesFind(rootNode, opts) {
		results = append(results, rootNode)
	}
	err = filepath.Walk(target.Key, func(p string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if p == target.Key {
			return nil
		}
		depth := strings.Count(strings.Trim(filepath.Clean(p), string(os.PathSeparator)), string(os.PathSeparator)) - rootDepth
		if opts.MaxDepth >= 0 && depth > opts.MaxDepth {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		node, err := statLocal(model.ResolvedPath{IsLocal: true, Key: p})
		if err != nil {
			return err
		}
		if matchesFind(node, opts) {
			results = append(results, node)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sortNodes(results)
	return results, nil
}

func max(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
