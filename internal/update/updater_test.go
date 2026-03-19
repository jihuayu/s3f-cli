package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestUpdateReplacesExecutableFromLatestRelease(t *testing.T) {
	assetName := "s3f_9.9.9_" + runtime.GOOS + "_" + runtime.GOARCH + archiveSuffix(runtime.GOOS)
	archive := makeArchive(t, runtime.GOOS, "updated-binary")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/jihuayu/s3f-cli/releases/latest":
			if got := r.Header.Get("Authorization"); got != "Bearer secret-token" {
				t.Fatalf("Authorization header = %q, want Bearer secret-token", got)
			}
			_ = json.NewEncoder(w).Encode(releaseResponse{
				TagName: "v9.9.9",
				Assets: []releaseAsset{{
					Name: assetName,
					URL:  serverURL(t, r) + "/asset",
				}},
			})
		case "/asset":
			if got := r.Header.Get("Authorization"); got != "Bearer secret-token" {
				t.Fatalf("asset Authorization header = %q, want Bearer secret-token", got)
			}
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	target := filepath.Join(t.TempDir(), binaryFilename(runtime.GOOS))
	if err := os.WriteFile(target, []byte("old-binary"), 0o755); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	updater := New(Config{
		APIBaseURL:     server.URL,
		CurrentVersion: "v0.1.0",
		TargetPath:     target,
		Token:          "secret-token",
		Client:         server.Client(),
	})

	result, err := updater.Update(context.Background())
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if !result.Updated {
		t.Fatalf("Update() Updated = false, want true")
	}
	if result.Version != "v9.9.9" {
		t.Fatalf("Version = %q, want v9.9.9", result.Version)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(got) != "updated-binary" {
		t.Fatalf("binary content = %q, want updated-binary", string(got))
	}
}

func TestUpdateSkipsWhenAlreadyLatest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(releaseResponse{
			TagName: "v1.2.3",
			Assets: []releaseAsset{{
				Name: "s3f_1.2.3_" + runtime.GOOS + "_" + runtime.GOARCH + archiveSuffix(runtime.GOOS),
				URL:  serverURL(t, r) + "/asset",
			}},
		})
	}))
	defer server.Close()

	target := filepath.Join(t.TempDir(), binaryFilename(runtime.GOOS))
	if err := os.WriteFile(target, []byte("unchanged"), 0o755); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	updater := New(Config{
		APIBaseURL:     server.URL,
		CurrentVersion: "1.2.3",
		TargetPath:     target,
		Client:         server.Client(),
	})

	result, err := updater.Update(context.Background())
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if result.Updated {
		t.Fatalf("Update() Updated = true, want false")
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(got) != "unchanged" {
		t.Fatalf("binary content = %q, want unchanged", string(got))
	}
}

func TestSelectAssetFailsForMissingPlatform(t *testing.T) {
	updater := New(Config{OS: "linux", Arch: "amd64"})
	_, err := updater.selectAsset(releaseResponse{
		TagName: "v1.0.0",
		Assets:  []releaseAsset{{Name: "s3f_1.0.0_darwin_arm64.tar.gz"}},
	})
	if err == nil {
		t.Fatalf("selectAsset() error = nil, want error")
	}
}

func TestExtractBinaryZipAndTarGz(t *testing.T) {
	for _, goos := range []string{"linux", "windows"} {
		t.Run(goos, func(t *testing.T) {
			archive := makeArchive(t, goos, "payload-"+goos)
			assetName := "s3f_1.0.0_" + goos + "_" + runtime.GOARCH + archiveSuffix(goos)
			binary, err := extractBinary(assetName, archive, binaryFilename(goos))
			if err != nil {
				t.Fatalf("extractBinary() error = %v", err)
			}
			if string(binary) != "payload-"+goos {
				t.Fatalf("binary = %q, want %q", string(binary), "payload-"+goos)
			}
		})
	}
}

func makeArchive(t *testing.T, goos, binaryBody string) []byte {
	t.Helper()
	if goos == "windows" {
		return makeZipArchive(t, binaryFilename(goos), binaryBody)
	}
	return makeTarGzArchive(t, binaryFilename(goos), binaryBody)
}

func makeTarGzArchive(t *testing.T, name, body string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	data := []byte(body)
	if err := tw.WriteHeader(&tar.Header{
		Name: name,
		Mode: 0o755,
		Size: int64(len(data)),
	}); err != nil {
		t.Fatalf("WriteHeader() error = %v", err)
	}
	if _, err := tw.Write(data); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("Close tar writer error = %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("Close gzip writer error = %v", err)
	}
	return buf.Bytes()
}

func makeZipArchive(t *testing.T, name, body string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	file, err := zw.Create(name)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := file.Write([]byte(body)); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("Close zip writer error = %v", err)
	}
	return buf.Bytes()
}

func serverURL(t *testing.T, r *http.Request) string {
	t.Helper()
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func TestNormalizeVersion(t *testing.T) {
	if got := normalizeVersion(" v1.2.3 "); got != "1.2.3" {
		t.Fatalf("normalizeVersion() = %q, want 1.2.3", got)
	}
}

func TestArchiveSuffix(t *testing.T) {
	if got := archiveSuffix("windows"); got != ".zip" {
		t.Fatalf("archiveSuffix(windows) = %q, want .zip", got)
	}
	if got := archiveSuffix("linux"); got != ".tar.gz" {
		t.Fatalf("archiveSuffix(linux) = %q, want .tar.gz", got)
	}
}

func TestBinaryFilename(t *testing.T) {
	if got := binaryFilename("windows"); got != "s3f.exe" {
		t.Fatalf("binaryFilename(windows) = %q, want s3f.exe", got)
	}
	if got := binaryFilename("darwin"); got != "s3f" {
		t.Fatalf("binaryFilename(darwin) = %q, want s3f", got)
	}
}

func TestTokenUsesEnvironment(t *testing.T) {
	t.Setenv("GH_TOKEN", "env-token")
	updater := New(Config{})
	if got := updater.token(); strings.TrimSpace(got) != "env-token" {
		t.Fatalf("token() = %q, want env-token", got)
	}
}
