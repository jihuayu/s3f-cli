package vfs

import (
	"testing"

	"s3f-cli/internal/model"
)

func TestResolveS3URI(t *testing.T) {
	resolver := NewPathResolver()

	resolved, err := resolver.Resolve(nil, "s3://bucket-a/foo/bar/")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	if resolved.Bucket != "bucket-a" {
		t.Fatalf("bucket = %q, want bucket-a", resolved.Bucket)
	}
	if resolved.Key != "foo/bar/" {
		t.Fatalf("key = %q, want foo/bar/", resolved.Key)
	}
	if !resolved.IsDirHint {
		t.Fatalf("expected directory hint to be true")
	}
}

func TestResolveAbsolutePath(t *testing.T) {
	resolver := NewPathResolver()

	resolved, err := resolver.Resolve(nil, "/bucket-a/foo/../bar.txt")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	if resolved.Bucket != "bucket-a" {
		t.Fatalf("bucket = %q, want bucket-a", resolved.Bucket)
	}
	if resolved.Key != "bar.txt" {
		t.Fatalf("key = %q, want bar.txt", resolved.Key)
	}
	if resolved.IsDirHint {
		t.Fatalf("expected file path, got directory hint")
	}
}

func TestResolveRelativePath(t *testing.T) {
	resolver := NewPathResolver()
	sess := &model.Session{Bucket: "bucket-a", Prefix: "foo/bar/"}

	resolved, err := resolver.Resolve(sess, "../baz")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	if resolved.Bucket != "bucket-a" {
		t.Fatalf("bucket = %q, want bucket-a", resolved.Bucket)
	}
	if resolved.Key != "foo/baz" {
		t.Fatalf("key = %q, want foo/baz", resolved.Key)
	}
}

func TestResolveRejectsEscapingBucketRoot(t *testing.T) {
	resolver := NewPathResolver()
	sess := &model.Session{Bucket: "bucket-a", Prefix: ""}

	_, err := resolver.Resolve(sess, "../../evil")
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
}
