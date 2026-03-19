package integration

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"s3f-cli/internal/model"
	"s3f-cli/internal/store"
	"s3f-cli/internal/vfs"
)

const (
	minioImage     = "minio/minio:latest"
	minioAccessKey = "minioadmin"
	minioSecretKey = "minioadmin"
)

var (
	minioHarness *minioEnv
	minioErr     error
)

type minioEnv struct {
	container string
	endpoint  string
}

func TestMain(m *testing.M) {
	minioHarness, minioErr = startMinIO()
	code := m.Run()
	if minioHarness != nil {
		_ = minioHarness.stop()
	}
	os.Exit(code)
}

func TestS3StoreCRUDAndMultipartAgainstMinIO(t *testing.T) {
	cfg, client, objectStore := requireMinIOStore(t)
	_ = cfg

	ctx := context.Background()
	bucket := createBucket(t, client)

	if _, err := objectStore.Put(ctx, bucket, "logs/app/", bytes.NewReader(nil), store.PutOptions{}); err != nil {
		t.Fatalf("Put(marker) error = %v", err)
	}
	if _, err := objectStore.Put(ctx, bucket, "logs/app/output.txt", strings.NewReader("hello minio"), store.PutOptions{}); err != nil {
		t.Fatalf("Put(file) error = %v", err)
	}

	head, err := objectStore.Head(ctx, bucket, "logs/app/output.txt")
	if err != nil {
		t.Fatalf("Head() error = %v", err)
	}
	if head.Size != int64(len("hello minio")) {
		t.Fatalf("Head size = %d, want %d", head.Size, len("hello minio"))
	}

	body, _, err := objectStore.Get(ctx, bucket, "logs/app/output.txt", store.GetOptions{Offset: 6, Length: 5})
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	got, err := io.ReadAll(body)
	body.Close()
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if string(got) != "minio" {
		t.Fatalf("range body = %q, want minio", string(got))
	}

	list, err := objectStore.ListPrefix(ctx, bucket, "logs/", "/", "", 1000)
	if err != nil {
		t.Fatalf("ListPrefix() error = %v", err)
	}
	if len(list.CommonDirs) != 1 || list.CommonDirs[0] != "logs/app/" {
		t.Fatalf("CommonDirs = %#v, want [logs/app/]", list.CommonDirs)
	}

	largeBody := bytes.Repeat([]byte("a"), 6*1024*1024)
	if _, err := objectStore.MultipartUpload(ctx, bucket, "large.bin", bytes.NewReader(largeBody), 5*1024*1024, store.PutOptions{}); err != nil {
		t.Fatalf("MultipartUpload() error = %v", err)
	}
	if _, err := objectStore.MultipartCopy(ctx, bucket, "large.bin", bucket, "large-copy.bin", 5*1024*1024, store.CopyOptions{}); err != nil {
		t.Fatalf("MultipartCopy() error = %v", err)
	}

	copyHead, err := objectStore.Head(ctx, bucket, "large-copy.bin")
	if err != nil {
		t.Fatalf("Head(copy) error = %v", err)
	}
	if copyHead.Size != int64(len(largeBody)) {
		t.Fatalf("copy size = %d, want %d", copyHead.Size, len(largeBody))
	}
}

func TestObjectVFSWorkflowAgainstMinIO(t *testing.T) {
	_, client, objectStore := requireMinIOStore(t)
	ctx := context.Background()
	bucket := createBucket(t, client)
	v := vfs.NewObjectVFS(objectStore)

	if err := v.MakeDirAll(ctx, model.ResolvedPath{Bucket: bucket, Key: "logs/app", IsDirHint: true}); err != nil {
		t.Fatalf("MakeDirAll() error = %v", err)
	}
	if _, err := objectStore.Put(ctx, bucket, "logs/app/output.txt", strings.NewReader("hello vfs"), store.PutOptions{}); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	if _, err := objectStore.Put(ctx, bucket, "logs/app/archive/", bytes.NewReader(nil), store.PutOptions{}); err != nil {
		t.Fatalf("Put(marker) error = %v", err)
	}
	if _, err := objectStore.Put(ctx, bucket, "logs/app/archive/2026-03-19.log", strings.NewReader("archived"), store.PutOptions{}); err != nil {
		t.Fatalf("Put(log) error = %v", err)
	}

	dirNode, err := v.Stat(ctx, model.ResolvedPath{Bucket: bucket, Key: "logs/app", IsDirHint: true}, vfs.StatOptions{AllowMarker: true})
	if err != nil {
		t.Fatalf("Stat(dir) error = %v", err)
	}
	if dirNode.Kind != model.NodeKindDir {
		t.Fatalf("dir kind = %q, want dir", dirNode.Kind)
	}

	listed, err := v.List(ctx, model.ResolvedPath{Bucket: bucket, Key: "logs/app", IsDirHint: true}, vfs.ListOptions{LongFormat: true})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(listed) != 2 {
		t.Fatalf("len(listed) = %d, want 2", len(listed))
	}

	reader, err := v.Read(ctx, model.ResolvedPath{Bucket: bucket, Key: "logs/app/output.txt"}, vfs.ReadOptions{})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	payload, err := io.ReadAll(reader)
	reader.Close()
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if string(payload) != "hello vfs" {
		t.Fatalf("payload = %q, want hello vfs", string(payload))
	}

	if err := v.Copy(ctx,
		model.ResolvedPath{Bucket: bucket, Key: "logs/app", IsDirHint: true},
		model.ResolvedPath{Bucket: bucket, Key: "backup/app", IsDirHint: true},
		vfs.CopyOptions{Recursive: true},
	); err != nil {
		t.Fatalf("Copy(tree) error = %v", err)
	}

	findResults, err := v.Find(ctx, model.ResolvedPath{Bucket: bucket, Key: "backup/app", IsDirHint: true}, vfs.FindOptions{
		MaxDepth:    3,
		NamePattern: "*.log",
		Type:        model.NodeKindFile,
	})
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	if len(findResults) != 1 {
		t.Fatalf("len(findResults) = %d, want 1", len(findResults))
	}

	moveResult, err := v.Move(ctx,
		model.ResolvedPath{Bucket: bucket, Key: "logs/app/output.txt"},
		model.ResolvedPath{Bucket: bucket, Key: "logs/app/output-moved.txt"},
		vfs.MoveOptions{},
	)
	if err != nil {
		t.Fatalf("Move() error = %v", err)
	}
	if moveResult.Partial {
		t.Fatalf("Move() partial = true, want false")
	}
	if _, err := objectStore.Head(ctx, bucket, "logs/app/output-moved.txt"); err != nil {
		t.Fatalf("Head(moved) error = %v", err)
	}
}

func requireMinIOStore(t *testing.T) (store.Config, *s3.Client, *store.S3Store) {
	t.Helper()
	if minioErr != nil {
		t.Skipf("minio unavailable: %v", minioErr)
	}

	cfg := store.Config{
		Endpoint:        minioHarness.endpoint,
		Region:          "us-east-1",
		AccessKeyID:     minioAccessKey,
		SecretAccessKey: minioSecretKey,
		UsePathStyle:    true,
	}

	client, err := store.NewClient(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	objectStore, err := store.NewS3Store(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewS3Store() error = %v", err)
	}
	return cfg, client, objectStore
}

func createBucket(t *testing.T, client *s3.Client) string {
	t.Helper()
	bucket := fmt.Sprintf("s3f-%d", time.Now().UnixNano())
	if _, err := client.CreateBucket(context.Background(), &s3.CreateBucketInput{
		Bucket: aws.String(bucket),
	}); err != nil {
		t.Fatalf("CreateBucket() error = %v", err)
	}
	return bucket
}

func startMinIO() (*minioEnv, error) {
	if _, err := exec.LookPath("docker"); err != nil {
		return nil, err
	}

	name := fmt.Sprintf("s3f-minio-%d", time.Now().UnixNano())
	args := []string{
		"run", "-d", "--rm",
		"--name", name,
		"-e", "MINIO_ROOT_USER=" + minioAccessKey,
		"-e", "MINIO_ROOT_PASSWORD=" + minioSecretKey,
		"-p", "127.0.0.1::9000",
		minioImage,
		"server", "/data",
	}
	if output, err := exec.Command("docker", args...).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("docker run minio: %w: %s", err, strings.TrimSpace(string(output)))
	}

	portOutput, err := exec.Command("docker", "port", name, "9000/tcp").CombinedOutput()
	if err != nil {
		_ = exec.Command("docker", "rm", "-f", name).Run()
		return nil, fmt.Errorf("docker port minio: %w: %s", err, strings.TrimSpace(string(portOutput)))
	}

	endpoint := "http://" + strings.TrimSpace(string(portOutput))
	if err := waitForMinIO(endpoint); err != nil {
		_ = exec.Command("docker", "rm", "-f", name).Run()
		return nil, err
	}

	return &minioEnv{
		container: name,
		endpoint:  endpoint,
	}, nil
}

func (m *minioEnv) stop() error {
	output, err := exec.Command("docker", "rm", "-f", m.container).CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker rm minio: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func waitForMinIO(endpoint string) error {
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(endpoint + "/minio/health/live")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("minio at %s did not become healthy before timeout", endpoint)
}
