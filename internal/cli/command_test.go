package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"s3f-cli/internal/model"
	"s3f-cli/internal/vfs"
)

type fakeVFS struct {
	statFn func(ctx context.Context, path model.ResolvedPath, opts vfs.StatOptions) (model.VNode, error)
	listFn func(ctx context.Context, path model.ResolvedPath, opts vfs.ListOptions) ([]model.VNode, error)
	readFn func(ctx context.Context, path model.ResolvedPath, opts vfs.ReadOptions) (io.ReadCloser, error)
	moveFn func(ctx context.Context, src, dst model.ResolvedPath, opts vfs.MoveOptions) (vfs.MoveResult, error)
}

func (f fakeVFS) Stat(ctx context.Context, path model.ResolvedPath, opts vfs.StatOptions) (model.VNode, error) {
	return f.statFn(ctx, path, opts)
}

func (f fakeVFS) List(ctx context.Context, path model.ResolvedPath, opts vfs.ListOptions) ([]model.VNode, error) {
	if f.listFn != nil {
		return f.listFn(ctx, path, opts)
	}
	return nil, nil
}

func (f fakeVFS) Read(ctx context.Context, path model.ResolvedPath, opts vfs.ReadOptions) (io.ReadCloser, error) {
	return f.readFn(ctx, path, opts)
}

func (f fakeVFS) MakeDirAll(context.Context, model.ResolvedPath) error {
	return nil
}

func (f fakeVFS) Copy(context.Context, model.ResolvedPath, model.ResolvedPath, vfs.CopyOptions) error {
	return nil
}

func (f fakeVFS) Move(ctx context.Context, src, dst model.ResolvedPath, opts vfs.MoveOptions) (vfs.MoveResult, error) {
	if f.moveFn != nil {
		return f.moveFn(ctx, src, dst, opts)
	}
	return vfs.MoveResult{}, nil
}

func (f fakeVFS) Find(context.Context, model.ResolvedPath, vfs.FindOptions) ([]model.VNode, error) {
	return nil, nil
}

func TestPwdWritesNormalizedCwd(t *testing.T) {
	var out bytes.Buffer
	cmd := PwdCommand{Out: &out}
	sess := &model.Session{Bucket: "bucket-a", Prefix: "foo/bar/"}

	if err := cmd.Run(context.Background(), sess, nil); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	got := strings.TrimSpace(out.String())
	if got != "/bucket-a/foo/bar/" {
		t.Fatalf("pwd output = %q, want /bucket-a/foo/bar/", got)
	}
}

func TestCdUpdatesSessionAfterStrictDirectoryCheck(t *testing.T) {
	cmd := CdCommand{
		Resolver: vfs.NewPathResolver(),
		VFS: fakeVFS{
			statFn: func(_ context.Context, path model.ResolvedPath, _ vfs.StatOptions) (model.VNode, error) {
				return model.VNode{
					Path:    path.RemoteDir(),
					Name:    "baz",
					Kind:    model.NodeKindDir,
					ModTime: time.Now(),
				}, nil
			},
			readFn: func(context.Context, model.ResolvedPath, vfs.ReadOptions) (io.ReadCloser, error) {
				return io.NopCloser(strings.NewReader("")), nil
			},
		},
	}

	sess := &model.Session{Bucket: "bucket-a", Prefix: "foo/bar/"}
	if err := cmd.Run(context.Background(), sess, []string{"../baz/"}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if sess.Cwd != "/bucket-a/foo/baz/" {
		t.Fatalf("cwd = %q, want /bucket-a/foo/baz/", sess.Cwd)
	}
	if sess.Prefix != "foo/baz/" {
		t.Fatalf("prefix = %q, want foo/baz/", sess.Prefix)
	}
}

func TestCdRejectsFileTargets(t *testing.T) {
	cmd := CdCommand{
		Resolver: vfs.NewPathResolver(),
		VFS: fakeVFS{
			statFn: func(_ context.Context, path model.ResolvedPath, _ vfs.StatOptions) (model.VNode, error) {
				return model.VNode{
					Path:    path.RemotePath(),
					Name:    "file.txt",
					Kind:    model.NodeKindFile,
					ModTime: time.Now(),
				}, nil
			},
			readFn: func(context.Context, model.ResolvedPath, vfs.ReadOptions) (io.ReadCloser, error) {
				return io.NopCloser(strings.NewReader("")), nil
			},
		},
	}

	sess := &model.Session{Bucket: "bucket-a", Prefix: ""}
	err := cmd.Run(context.Background(), sess, []string{"file.txt"})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}

	var pathErr *model.Error
	if !errors.As(err, &pathErr) || pathErr.Code != model.ErrInvalidPath {
		t.Fatalf("expected invalid path error, got %v", err)
	}
}

func TestLsLongFormatWritesSyntheticColumns(t *testing.T) {
	var out bytes.Buffer
	cmd := LsCommand{
		Resolver: vfs.NewPathResolver(),
		VFS: fakeVFS{
			listFn: func(_ context.Context, _ model.ResolvedPath, _ vfs.ListOptions) ([]model.VNode, error) {
				return []model.VNode{{
					Name:          "logs/",
					Kind:          model.NodeKindDir,
					Size:          0,
					ModTime:       time.Date(2026, 3, 19, 10, 20, 0, 0, time.UTC),
					SyntheticMode: "drwxr-xr-x",
				}}, nil
			},
		},
		Out:  &out,
		Long: true,
	}

	sess := &model.Session{Bucket: "bucket-a", Prefix: ""}
	if err := cmd.Run(context.Background(), sess, nil); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "drwxr-xr-x") || !strings.Contains(got, "logs/") {
		t.Fatalf("ls -l output = %q, want synthetic mode and name", got)
	}
}

func TestCatWritesObjectBody(t *testing.T) {
	var out bytes.Buffer
	cmd := CatCommand{
		Resolver: vfs.NewPathResolver(),
		VFS: fakeVFS{
			readFn: func(_ context.Context, _ model.ResolvedPath, _ vfs.ReadOptions) (io.ReadCloser, error) {
				return io.NopCloser(strings.NewReader("hello world")), nil
			},
		},
		Out: &out,
	}

	sess := &model.Session{Bucket: "bucket-a", Prefix: ""}
	if err := cmd.Run(context.Background(), sess, []string{"hello.txt"}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if got := out.String(); got != "hello world" {
		t.Fatalf("cat output = %q, want hello world", got)
	}
}

func TestMvReturnsNonAtomicMoveErrorOnPartialSuccess(t *testing.T) {
	cmd := MvCommand{
		Resolver: vfs.NewPathResolver(),
		VFS: fakeVFS{
			moveFn: func(_ context.Context, _, _ model.ResolvedPath, _ vfs.MoveOptions) (vfs.MoveResult, error) {
				return vfs.MoveResult{Partial: true}, nil
			},
		},
	}

	sess := &model.Session{Bucket: "bucket-a", Prefix: ""}
	err := cmd.Run(context.Background(), sess, []string{"src.txt", "dst.txt"})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}

	var moveErr *model.Error
	if !errors.As(err, &moveErr) || moveErr.Code != model.ErrNonAtomicMove {
		t.Fatalf("expected non-atomic move error, got %v", err)
	}
}
