package update

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeBackend struct {
	detectRelease release
	found         bool
	detectErr     error
	updateErr     error
	targetPath    string
	updatedTo     string
}

type fakeRelease struct {
	version   string
	assetName string
	latest    bool
}

func (b *fakeBackend) DetectLatest(context.Context) (release, bool, error) {
	return b.detectRelease, b.found, b.detectErr
}

func (b *fakeBackend) UpdateTo(_ context.Context, rel release, cmdPath string) error {
	if b.updateErr != nil {
		return b.updateErr
	}
	b.updatedTo = cmdPath
	return nil
}

func (b *fakeBackend) ExecutablePath() (string, error) {
	if b.targetPath == "" {
		return "", errors.New("missing target path")
	}
	return b.targetPath, nil
}

func (r fakeRelease) Version() string {
	return r.version
}

func (r fakeRelease) AssetName() string {
	return r.assetName
}

func (r fakeRelease) LessOrEqual(other string) bool {
	return !r.latest || normalizeVersion(r.version) == normalizeVersion(other)
}

func TestCheckFindsNewRelease(t *testing.T) {
	updater := New(Config{CurrentVersion: "v0.1.0"})
	updater.backend = &fakeBackend{
		found:         true,
		targetPath:    "/tmp/s3f",
		detectRelease: fakeRelease{version: "0.2.0", assetName: "s3f_linux_amd64.tar.gz", latest: true},
	}

	result, err := updater.Check(context.Background())
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if !result.Updated {
		t.Fatalf("Updated = false, want true")
	}
	if result.Version != "0.2.0" {
		t.Fatalf("Version = %q, want 0.2.0", result.Version)
	}
}

func TestCheckReportsUpToDate(t *testing.T) {
	updater := New(Config{CurrentVersion: "v0.2.0"})
	updater.backend = &fakeBackend{
		found:         true,
		targetPath:    "/tmp/s3f",
		detectRelease: fakeRelease{version: "0.2.0", assetName: "s3f_linux_amd64.tar.gz", latest: false},
	}

	result, err := updater.Check(context.Background())
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if result.Updated {
		t.Fatalf("Updated = true, want false")
	}
}

func TestUpdateAppliesLatestRelease(t *testing.T) {
	targetPath := filepath.Join(t.TempDir(), "s3f")
	if err := os.WriteFile(targetPath, []byte("old"), 0o755); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	backend := &fakeBackend{
		found:         true,
		targetPath:    targetPath,
		detectRelease: fakeRelease{version: "1.0.0", assetName: "s3f_darwin_arm64.tar.gz", latest: true},
	}
	updater := New(Config{CurrentVersion: "0.9.0"})
	updater.backend = backend

	result, err := updater.Update(context.Background())
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if !result.Updated {
		t.Fatalf("Updated = false, want true")
	}
	if backend.updatedTo != targetPath {
		t.Fatalf("updatedTo = %q, want %q", backend.updatedTo, targetPath)
	}
}

func TestUpdateReturnsErrorWhenNoAssetMatches(t *testing.T) {
	updater := New(Config{CurrentVersion: "0.1.0"})
	updater.backend = &fakeBackend{found: false}

	_, err := updater.Update(context.Background())
	if err == nil {
		t.Fatalf("Update() error = nil, want error")
	}
}

func TestTokenUsesEnvironment(t *testing.T) {
	t.Setenv("GH_TOKEN", "env-token")
	updater := New(Config{})
	if got := updater.token(); got != "env-token" {
		t.Fatalf("token() = %q, want env-token", got)
	}
}

func TestNormalizeVersion(t *testing.T) {
	if got := normalizeVersion(" v1.2.3 "); got != "1.2.3" {
		t.Fatalf("normalizeVersion() = %q, want 1.2.3", got)
	}
}

func TestTokenUsesExplicitValue(t *testing.T) {
	updater := New(Config{Token: "explicit-token"})
	if got := updater.token(); got != "explicit-token" {
		t.Fatalf("token() = %q, want explicit-token", got)
	}
}

func TestTargetPathUsesConfiguredValue(t *testing.T) {
	updater := New(Config{TargetPath: "/custom/s3f"})
	path := updater.targetPath(&fakeBackend{targetPath: "/ignored"})
	if path != "/custom/s3f" {
		t.Fatalf("targetPath() = %q, want /custom/s3f", path)
	}
}

func TestUpdatePropagatesBackendError(t *testing.T) {
	backend := &fakeBackend{
		found:         true,
		targetPath:    "/tmp/s3f",
		detectRelease: fakeRelease{version: "1.0.0", assetName: "s3f_linux_amd64.tar.gz", latest: true},
		updateErr:     errors.New("boom"),
	}
	updater := New(Config{CurrentVersion: "0.1.0"})
	updater.backend = backend

	_, err := updater.Update(context.Background())
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("Update() error = %v, want backend error", err)
	}
}
