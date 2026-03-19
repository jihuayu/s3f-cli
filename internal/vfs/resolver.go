package vfs

import (
	"path"
	"path/filepath"
	"strings"

	"s3f-cli/internal/model"
)

type DefaultPathResolver struct{}

func NewPathResolver() DefaultPathResolver {
	return DefaultPathResolver{}
}

func (DefaultPathResolver) Resolve(sess *model.Session, input string) (model.ResolvedPath, error) {
	raw := strings.TrimSpace(input)
	if raw == "" {
		if sess == nil || sess.Bucket == "" {
			return model.ResolvedPath{}, model.InvalidPath("resolve", input, "empty path without an active bucket")
		}

		current := model.ResolvedPath{
			Scheme:    "s3",
			Bucket:    sess.Bucket,
			Key:       model.EnsureDirKey(sess.Prefix),
			IsDirHint: true,
		}
		return current, nil
	}

	if strings.HasPrefix(raw, "s3://") {
		return resolveS3URI(raw)
	}

	if strings.HasPrefix(raw, "file://") {
		return resolveLocal(raw)
	}

	if strings.HasPrefix(raw, "/") {
		return resolveAbsolute(raw)
	}

	if sess == nil || sess.Bucket == "" {
		return model.ResolvedPath{
			Scheme:    "file",
			Key:       filepath.Clean(raw),
			IsDirHint: strings.HasSuffix(raw, "/"),
			IsLocal:   true,
		}, nil
	}

	return resolveRelative(sess, raw)
}

func resolveS3URI(raw string) (model.ResolvedPath, error) {
	withoutScheme := strings.TrimPrefix(raw, "s3://")
	parts := strings.SplitN(withoutScheme, "/", 2)
	if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
		return model.ResolvedPath{}, model.InvalidPath("resolve", raw, "missing bucket in s3 uri")
	}

	bucket := parts[0]
	key := ""
	if len(parts) == 2 {
		key = parts[1]
	}

	isDir := strings.HasSuffix(raw, "/")
	key = model.NormalizeKey(key, isDir)
	return model.ResolvedPath{
		Scheme:    "s3",
		Bucket:    bucket,
		Key:       key,
		IsDirHint: isDir || key == "",
	}, nil
}

func resolveAbsolute(raw string) (model.ResolvedPath, error) {
	trimmed := strings.TrimPrefix(raw, "/")
	if trimmed == "" {
		return model.ResolvedPath{}, model.InvalidPath("resolve", raw, "absolute path must include a bucket name")
	}

	parts := strings.SplitN(trimmed, "/", 2)
	bucket := parts[0]
	if bucket == "" {
		return model.ResolvedPath{}, model.InvalidPath("resolve", raw, "absolute path must include a bucket name")
	}

	key := ""
	if len(parts) == 2 {
		key = parts[1]
	}

	isDir := strings.HasSuffix(raw, "/")
	key = model.NormalizeKey(key, isDir)
	return model.ResolvedPath{
		Scheme:    "s3",
		Bucket:    bucket,
		Key:       key,
		IsDirHint: isDir || key == "",
	}, nil
}

func resolveRelative(sess *model.Session, raw string) (model.ResolvedPath, error) {
	currentDir := "/" + sess.Bucket + "/" + model.EnsureDirKey(sess.Prefix)
	cleaned := path.Clean(path.Join(currentDir, raw))
	cleaned = strings.TrimPrefix(cleaned, "/")
	parts := strings.SplitN(cleaned, "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		return model.ResolvedPath{}, model.InvalidPath("resolve", raw, "relative path escaped bucket root")
	}

	bucket := parts[0]
	if bucket != sess.Bucket {
		return model.ResolvedPath{}, model.InvalidPath("resolve", raw, "relative path escaped bucket root")
	}

	key := ""
	if len(parts) == 2 {
		key = parts[1]
	}

	isDir := strings.HasSuffix(raw, "/") || raw == "." || raw == ".." || strings.HasSuffix(raw, "/.")
	key = model.NormalizeKey(key, isDir)
	return model.ResolvedPath{
		Scheme:    "s3",
		Bucket:    bucket,
		Key:       key,
		IsDirHint: isDir || key == "",
	}, nil
}

func resolveLocal(raw string) (model.ResolvedPath, error) {
	pathValue := strings.TrimPrefix(raw, "file://")
	if pathValue == "" {
		return model.ResolvedPath{}, model.InvalidPath("resolve", raw, "missing local file path")
	}
	return model.ResolvedPath{
		Scheme:    "file",
		Key:       filepath.Clean(pathValue),
		IsDirHint: strings.HasSuffix(raw, "/"),
		IsLocal:   true,
	}, nil
}
