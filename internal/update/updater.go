package update

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	selfupdate "github.com/creativeprojects/go-selfupdate"
)

const (
	DefaultOwner    = "jihuayu"
	DefaultRepo     = "s3f-cli"
	DefaultChecksum = "checksums.txt"
)

type Config struct {
	Owner            string
	Repo             string
	CurrentVersion   string
	TargetPath       string
	Token            string
	ChecksumFilename string
}

type Updater struct {
	cfg     Config
	backend backend
}

type Result struct {
	Updated    bool
	Version    string
	AssetName  string
	TargetPath string
}

type backend interface {
	DetectLatest(ctx context.Context) (release, bool, error)
	UpdateTo(ctx context.Context, rel release, cmdPath string) error
	ExecutablePath() (string, error)
}

type release interface {
	Version() string
	AssetName() string
	LessOrEqual(other string) bool
}

type selfupdateBackend struct {
	updater    *selfupdate.Updater
	repository selfupdate.Repository
}

type selfupdateRelease struct {
	inner *selfupdate.Release
}

func New(cfg Config) *Updater {
	if cfg.Owner == "" {
		cfg.Owner = DefaultOwner
	}
	if cfg.Repo == "" {
		cfg.Repo = DefaultRepo
	}
	if cfg.ChecksumFilename == "" {
		cfg.ChecksumFilename = DefaultChecksum
	}
	return &Updater{cfg: cfg}
}

func (u *Updater) Check(ctx context.Context) (Result, error) {
	backend, err := u.getBackend()
	if err != nil {
		return Result{}, err
	}

	latest, found, err := backend.DetectLatest(ctx)
	if err != nil {
		return Result{}, err
	}
	if !found {
		return Result{}, errors.New("no compatible release asset found for this platform")
	}

	if latest.LessOrEqual(normalizeVersion(u.cfg.CurrentVersion)) {
		return Result{
			Updated:    false,
			Version:    latest.Version(),
			AssetName:  latest.AssetName(),
			TargetPath: u.targetPath(backend),
		}, nil
	}

	return Result{
		Updated:    true,
		Version:    latest.Version(),
		AssetName:  latest.AssetName(),
		TargetPath: u.targetPath(backend),
	}, nil
}

func (u *Updater) Update(ctx context.Context) (Result, error) {
	backend, err := u.getBackend()
	if err != nil {
		return Result{}, err
	}

	latest, found, err := backend.DetectLatest(ctx)
	if err != nil {
		return Result{}, err
	}
	if !found {
		return Result{}, errors.New("no compatible release asset found for this platform")
	}

	if latest.LessOrEqual(normalizeVersion(u.cfg.CurrentVersion)) {
		return Result{
			Updated:    false,
			Version:    latest.Version(),
			AssetName:  latest.AssetName(),
			TargetPath: u.targetPath(backend),
		}, nil
	}

	targetPath := u.targetPath(backend)
	if targetPath == "" {
		return Result{}, errors.New("update target path is empty")
	}
	if err := backend.UpdateTo(ctx, latest, targetPath); err != nil {
		return Result{}, err
	}

	return Result{
		Updated:    true,
		Version:    latest.Version(),
		AssetName:  latest.AssetName(),
		TargetPath: targetPath,
	}, nil
}

func (u *Updater) getBackend() (backend, error) {
	if u.backend != nil {
		return u.backend, nil
	}

	source, err := selfupdate.NewGitHubSource(selfupdate.GitHubConfig{
		APIToken: u.token(),
	})
	if err != nil {
		return nil, err
	}

	updater, err := selfupdate.NewUpdater(selfupdate.Config{
		Source: source,
		Validator: &selfupdate.ChecksumValidator{
			UniqueFilename: u.cfg.ChecksumFilename,
		},
	})
	if err != nil {
		return nil, err
	}

	u.backend = &selfupdateBackend{
		updater:    updater,
		repository: selfupdate.NewRepositorySlug(u.cfg.Owner, u.cfg.Repo),
	}
	return u.backend, nil
}

func (u *Updater) token() string {
	if token := strings.TrimSpace(u.cfg.Token); token != "" {
		return token
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

func (u *Updater) targetPath(backend backend) string {
	if u.cfg.TargetPath != "" {
		return u.cfg.TargetPath
	}
	path, err := backend.ExecutablePath()
	if err != nil {
		return ""
	}
	return path
}

func normalizeVersion(version string) string {
	return strings.TrimPrefix(strings.TrimSpace(version), "v")
}

func (b *selfupdateBackend) DetectLatest(ctx context.Context) (release, bool, error) {
	rel, found, err := b.updater.DetectLatest(ctx, b.repository)
	if err != nil || !found || rel == nil {
		return nil, found, err
	}
	return selfupdateRelease{inner: rel}, true, nil
}

func (b *selfupdateBackend) UpdateTo(ctx context.Context, rel release, cmdPath string) error {
	casted, ok := rel.(selfupdateRelease)
	if !ok {
		return fmt.Errorf("unsupported release type %T", rel)
	}
	return b.updater.UpdateTo(ctx, casted.inner, cmdPath)
}

func (b *selfupdateBackend) ExecutablePath() (string, error) {
	return selfupdate.ExecutablePath()
}

func (r selfupdateRelease) Version() string {
	return r.inner.Version()
}

func (r selfupdateRelease) AssetName() string {
	return r.inner.AssetName
}

func (r selfupdateRelease) LessOrEqual(other string) bool {
	return r.inner.LessOrEqual(other)
}
