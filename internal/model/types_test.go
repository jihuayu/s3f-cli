package model

import "testing"

func TestEnsureDirKey(t *testing.T) {
	if got := EnsureDirKey("foo/bar"); got != "foo/bar/" {
		t.Fatalf("EnsureDirKey() = %q, want foo/bar/", got)
	}
	if got := EnsureDirKey("/"); got != "" {
		t.Fatalf("EnsureDirKey(/) = %q, want empty", got)
	}
}

func TestNormalizeKey(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		isDir bool
		want  string
	}{
		{name: "file", key: "foo/../bar.txt", want: "bar.txt"},
		{name: "dir", key: "foo/../bar", isDir: true, want: "bar/"},
		{name: "root", key: ".", want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := NormalizeKey(tc.key, tc.isDir); got != tc.want {
				t.Fatalf("NormalizeKey() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSessionApply(t *testing.T) {
	sess := &Session{Bucket: "old", Prefix: "old/"}
	sess.Apply(ResolvedPath{Bucket: "bucket-a", Key: "foo/bar"})

	if sess.Bucket != "bucket-a" {
		t.Fatalf("Bucket = %q, want bucket-a", sess.Bucket)
	}
	if sess.Prefix != "foo/bar/" {
		t.Fatalf("Prefix = %q, want foo/bar/", sess.Prefix)
	}
	if sess.Cwd != "/bucket-a/foo/bar/" {
		t.Fatalf("Cwd = %q, want /bucket-a/foo/bar/", sess.Cwd)
	}
}
