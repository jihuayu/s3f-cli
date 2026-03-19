package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	DefaultOwner   = "jihuayu"
	DefaultRepo    = "s3f-cli"
	DefaultAPIBase = "https://api.github.com"
	BinaryName     = "s3f"
)

type Config struct {
	Owner          string
	Repo           string
	APIBaseURL     string
	CurrentVersion string
	TargetPath     string
	Token          string
	OS             string
	Arch           string
	Client         *http.Client
}

type Updater struct {
	cfg Config
}

type Result struct {
	Updated    bool
	Version    string
	AssetName  string
	TargetPath string
}

type releaseResponse struct {
	TagName string         `json:"tag_name"`
	Assets  []releaseAsset `json:"assets"`
}

type releaseAsset struct {
	ID                 int64  `json:"id"`
	Name               string `json:"name"`
	URL                string `json:"url"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func New(cfg Config) *Updater {
	if cfg.Owner == "" {
		cfg.Owner = DefaultOwner
	}
	if cfg.Repo == "" {
		cfg.Repo = DefaultRepo
	}
	if cfg.APIBaseURL == "" {
		cfg.APIBaseURL = DefaultAPIBase
	}
	if cfg.OS == "" {
		cfg.OS = runtime.GOOS
	}
	if cfg.Arch == "" {
		cfg.Arch = runtime.GOARCH
	}
	if cfg.Client == nil {
		cfg.Client = http.DefaultClient
	}
	return &Updater{cfg: cfg}
}

func (u *Updater) Update(ctx context.Context) (Result, error) {
	release, result, err := u.checkLatest(ctx)
	if err != nil {
		return Result{}, err
	}
	if !result.Updated {
		return result, nil
	}

	asset, err := u.selectAsset(release)
	if err != nil {
		return Result{}, err
	}

	body, err := u.downloadAsset(ctx, asset)
	if err != nil {
		return Result{}, err
	}

	binary, err := extractBinary(asset.Name, body, binaryFilename(u.cfg.OS))
	if err != nil {
		return Result{}, err
	}

	if err := u.install(binary); err != nil {
		return Result{}, err
	}

	return Result{
		Updated:    true,
		Version:    release.TagName,
		AssetName:  asset.Name,
		TargetPath: u.targetPath(),
	}, nil
}

func (u *Updater) Check(ctx context.Context) (Result, error) {
	_, result, err := u.checkLatest(ctx)
	return result, err
}

func (u *Updater) fetchLatestRelease(ctx context.Context) (releaseResponse, error) {
	url := strings.TrimRight(u.cfg.APIBaseURL, "/") + "/repos/" + u.cfg.Owner + "/" + u.cfg.Repo + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return releaseResponse{}, err
	}
	u.applyHeaders(req, "application/vnd.github+json")

	resp, err := u.cfg.Client.Do(req)
	if err != nil {
		return releaseResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return releaseResponse{}, fmt.Errorf("fetch latest release: unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var release releaseResponse
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return releaseResponse{}, err
	}
	if release.TagName == "" {
		return releaseResponse{}, errors.New("fetch latest release: missing tag name")
	}
	return release, nil
}

func (u *Updater) checkLatest(ctx context.Context) (releaseResponse, Result, error) {
	release, err := u.fetchLatestRelease(ctx)
	if err != nil {
		return releaseResponse{}, Result{}, err
	}

	latest := normalizeVersion(release.TagName)
	current := normalizeVersion(u.cfg.CurrentVersion)
	if latest != "" && latest == current {
		return release, Result{
			Updated:    false,
			Version:    release.TagName,
			TargetPath: u.targetPath(),
		}, nil
	}

	return release, Result{
		Updated:    true,
		Version:    release.TagName,
		TargetPath: u.targetPath(),
	}, nil
}

func (u *Updater) selectAsset(release releaseResponse) (releaseAsset, error) {
	suffix := archiveSuffix(u.cfg.OS)
	pattern := "_" + u.cfg.OS + "_" + u.cfg.Arch + suffix
	for _, asset := range release.Assets {
		if strings.HasSuffix(asset.Name, pattern) {
			return asset, nil
		}
	}
	return releaseAsset{}, fmt.Errorf("no release asset matches %s/%s", u.cfg.OS, u.cfg.Arch)
}

func (u *Updater) downloadAsset(ctx context.Context, asset releaseAsset) ([]byte, error) {
	downloadURL := asset.URL
	accept := "application/octet-stream"
	if downloadURL == "" {
		downloadURL = asset.BrowserDownloadURL
		accept = ""
	}
	if downloadURL == "" {
		return nil, fmt.Errorf("release asset %s has no download url", asset.Name)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return nil, err
	}
	u.applyHeaders(req, accept)

	resp, err := u.cfg.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("download asset %s: unexpected status %d: %s", asset.Name, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	return io.ReadAll(resp.Body)
}

func (u *Updater) install(binary []byte) error {
	targetPath := u.targetPath()
	if targetPath == "" {
		return errors.New("update target path is empty")
	}

	info, err := os.Stat(targetPath)
	if err != nil {
		return fmt.Errorf("stat target executable: %w", err)
	}

	if u.cfg.OS == "windows" {
		return installWindows(targetPath, binary, info.Mode())
	}
	return installUnix(targetPath, binary, info.Mode())
}

func installUnix(targetPath string, binary []byte, mode os.FileMode) error {
	dir := filepath.Dir(targetPath)
	tempFile, err := os.CreateTemp(dir, ".s3f-update-*")
	if err != nil {
		return err
	}
	tempPath := tempFile.Name()
	defer os.Remove(tempPath)

	if _, err := tempFile.Write(binary); err != nil {
		tempFile.Close()
		return err
	}
	if err := tempFile.Chmod(mode); err != nil {
		tempFile.Close()
		return err
	}
	if err := tempFile.Close(); err != nil {
		return err
	}

	return os.Rename(tempPath, targetPath)
}

func installWindows(targetPath string, binary []byte, mode os.FileMode) error {
	newPath := targetPath + ".new"
	if err := os.WriteFile(newPath, binary, mode); err != nil {
		return err
	}

	scriptFile, err := os.CreateTemp(filepath.Dir(targetPath), "s3f-update-*.cmd")
	if err != nil {
		return err
	}
	scriptPath := scriptFile.Name()
	script := fmt.Sprintf(`@echo off
ping 127.0.0.1 -n 2 > nul
move /Y "%s" "%s.old" > nul 2>&1
move /Y "%s" "%s" > nul
del "%s.old" > nul 2>&1
del "%%~f0"
`, targetPath, targetPath, newPath, targetPath, targetPath)
	if _, err := scriptFile.WriteString(script); err != nil {
		scriptFile.Close()
		return err
	}
	if err := scriptFile.Close(); err != nil {
		return err
	}

	cmd := exec.Command("cmd", "/C", "start", "", "/min", scriptPath)
	return cmd.Start()
}

func extractBinary(assetName string, archive []byte, expectedBinary string) ([]byte, error) {
	switch {
	case strings.HasSuffix(assetName, ".tar.gz"):
		return extractTarGz(archive, expectedBinary)
	case strings.HasSuffix(assetName, ".zip"):
		return extractZip(archive, expectedBinary)
	default:
		return nil, fmt.Errorf("unsupported release archive format: %s", assetName)
	}
}

func extractTarGz(archive []byte, expectedBinary string) ([]byte, error) {
	gzipReader, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	defer gzipReader.Close()

	tarReader := tar.NewReader(gzipReader)
	for {
		header, err := tarReader.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
		if header.FileInfo().IsDir() {
			continue
		}
		if filepath.Base(header.Name) == expectedBinary {
			return io.ReadAll(tarReader)
		}
	}
	return nil, fmt.Errorf("binary %s not found in tar.gz archive", expectedBinary)
}

func extractZip(archive []byte, expectedBinary string) ([]byte, error) {
	readerAt := bytes.NewReader(archive)
	zipReader, err := zip.NewReader(readerAt, int64(len(archive)))
	if err != nil {
		return nil, err
	}
	for _, file := range zipReader.File {
		if file.FileInfo().IsDir() {
			continue
		}
		if filepath.Base(file.Name) != expectedBinary {
			continue
		}
		rc, err := file.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return io.ReadAll(rc)
	}
	return nil, fmt.Errorf("binary %s not found in zip archive", expectedBinary)
}

func (u *Updater) targetPath() string {
	if u.cfg.TargetPath != "" {
		return u.cfg.TargetPath
	}
	path, err := os.Executable()
	if err != nil {
		return ""
	}
	return path
}

func (u *Updater) applyHeaders(req *http.Request, accept string) {
	req.Header.Set("User-Agent", "s3f-cli-updater")
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	if token := u.token(); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
}

func (u *Updater) token() string {
	if u.cfg.Token != "" {
		return u.cfg.Token
	}
	if token := strings.TrimSpace(os.Getenv("GH_TOKEN")); token != "" {
		return token
	}
	if token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); token != "" {
		return token
	}
	if path, err := exec.LookPath("gh"); err == nil && path != "" {
		output, err := exec.Command(path, "auth", "token").Output()
		if err == nil {
			return strings.TrimSpace(string(output))
		}
	}
	return ""
}

func archiveSuffix(goos string) string {
	if goos == "windows" {
		return ".zip"
	}
	return ".tar.gz"
}

func binaryFilename(goos string) string {
	if goos == "windows" {
		return BinaryName + ".exe"
	}
	return BinaryName
}

func normalizeVersion(version string) string {
	return strings.TrimPrefix(strings.TrimSpace(version), "v")
}
