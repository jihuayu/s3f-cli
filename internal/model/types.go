package model

import (
	"path"
	"strings"
	"time"
)

type NodeKind string

const (
	NodeKindFile NodeKind = "file"
	NodeKindDir  NodeKind = "dir"
)

type Session struct {
	Bucket   string
	Prefix   string
	Cwd      string
	Endpoint string
}

func (s *Session) Clone() *Session {
	if s == nil {
		return &Session{}
	}

	copy := *s
	return &copy
}

func (s *Session) Apply(resolved ResolvedPath) {
	if s == nil || resolved.IsLocal {
		return
	}

	s.Bucket = resolved.Bucket
	s.Prefix = EnsureDirKey(resolved.Key)
	s.Cwd = resolved.RemoteDir()
}

func (s *Session) EnsureDefaults() {
	if s == nil {
		return
	}

	if s.Cwd == "" && s.Bucket != "" {
		s.Cwd = "/" + s.Bucket + "/" + EnsureDirKey(s.Prefix)
	}
}

type ResolvedPath struct {
	Scheme    string
	Bucket    string
	Key       string
	IsDirHint bool
	IsLocal   bool
}

func (r ResolvedPath) RemotePath() string {
	if r.IsLocal {
		return r.Key
	}

	if r.Bucket == "" {
		return "/"
	}

	if r.Key == "" {
		return "/" + r.Bucket
	}

	return "/" + r.Bucket + "/" + r.Key
}

func (r ResolvedPath) RemoteDir() string {
	if r.IsLocal {
		return r.Key
	}

	if r.Bucket == "" {
		return "/"
	}

	dirKey := EnsureDirKey(r.Key)
	if dirKey == "" {
		return "/" + r.Bucket + "/"
	}

	return "/" + r.Bucket + "/" + dirKey
}

type VNode struct {
	Path          string
	Name          string
	Kind          NodeKind
	Size          int64
	ModTime       time.Time
	StorageClass  string
	ETag          string
	VersionID     string
	SyntheticMode string
}

func EnsureDirKey(key string) string {
	trimmed := strings.Trim(key, "/")
	if trimmed == "" {
		return ""
	}
	return trimmed + "/"
}

func NormalizeKey(key string, isDir bool) string {
	cleaned := path.Clean("/" + strings.TrimSpace(key))
	cleaned = strings.TrimPrefix(cleaned, "/")
	if cleaned == "." {
		cleaned = ""
	}
	if isDir {
		return EnsureDirKey(cleaned)
	}
	return strings.TrimSuffix(cleaned, "/")
}
