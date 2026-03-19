package store

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

type Config struct {
	Endpoint        string
	Region          string
	AccessKeyID     string
	SecretAccessKey string
	UsePathStyle    bool
}

type S3Store struct {
	client *s3.Client
}

func NewClient(ctx context.Context, cfg Config) (*s3.Client, error) {
	if cfg.Region == "" {
		cfg.Region = "us-east-1"
	}

	loadOptions := []func(*config.LoadOptions) error{
		config.WithRegion(cfg.Region),
	}
	if cfg.AccessKeyID != "" || cfg.SecretAccessKey != "" {
		loadOptions = append(loadOptions, config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, "")))
	}

	awsCfg, err := config.LoadDefaultConfig(ctx, loadOptions...)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.UsePathStyle = cfg.UsePathStyle
		if cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		}
	})
	return client, nil
}

func NewS3Store(ctx context.Context, cfg Config) (*S3Store, error) {
	client, err := NewClient(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return &S3Store{client: client}, nil
}

func (s *S3Store) ListPrefix(ctx context.Context, bucket, prefix, delimiter, token string, limit int32) (ListResult, error) {
	input := &s3.ListObjectsV2Input{
		Bucket: aws.String(bucket),
		Prefix: aws.String(prefix),
	}
	if delimiter != "" {
		input.Delimiter = aws.String(delimiter)
	}
	if token != "" {
		input.ContinuationToken = aws.String(token)
	}
	if limit > 0 {
		input.MaxKeys = aws.Int32(limit)
	}

	output, err := s.client.ListObjectsV2(ctx, input)
	if err != nil {
		if isAWSNotFound(err) {
			return ListResult{}, ErrNotFound
		}
		return ListResult{}, err
	}

	result := ListResult{
		CommonDirs: make([]string, 0, len(output.CommonPrefixes)),
		Objects:    make([]ObjectInfo, 0, len(output.Contents)),
		Truncated:  aws.ToBool(output.IsTruncated),
	}
	if output.NextContinuationToken != nil {
		result.NextToken = aws.ToString(output.NextContinuationToken)
	}
	for _, prefix := range output.CommonPrefixes {
		result.CommonDirs = append(result.CommonDirs, aws.ToString(prefix.Prefix))
	}
	for _, object := range output.Contents {
		info := ObjectInfo{
			Key:          aws.ToString(object.Key),
			Size:         aws.ToInt64(object.Size),
			LastModified: aws.ToTime(object.LastModified),
			StorageClass: string(object.StorageClass),
			ETag:         strings.Trim(aws.ToString(object.ETag), "\""),
			IsMarker:     strings.HasSuffix(aws.ToString(object.Key), "/") && aws.ToInt64(object.Size) == 0,
		}
		result.Objects = append(result.Objects, info)
	}
	return result, nil
}

func (s *S3Store) Head(ctx context.Context, bucket, key string) (ObjectInfo, error) {
	output, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		if isAWSNotFound(err) {
			return ObjectInfo{}, ErrNotFound
		}
		return ObjectInfo{}, err
	}

	return ObjectInfo{
		Key:          key,
		Size:         aws.ToInt64(output.ContentLength),
		LastModified: aws.ToTime(output.LastModified),
		StorageClass: string(output.StorageClass),
		ETag:         strings.Trim(aws.ToString(output.ETag), "\""),
		VersionID:    aws.ToString(output.VersionId),
		IsMarker:     strings.HasSuffix(key, "/") && aws.ToInt64(output.ContentLength) == 0,
	}, nil
}

func (s *S3Store) Get(ctx context.Context, bucket, key string, opts GetOptions) (io.ReadCloser, ObjectInfo, error) {
	input := &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	}
	if opts.Length > 0 {
		end := opts.Offset + opts.Length - 1
		input.Range = aws.String(fmt.Sprintf("bytes=%d-%d", opts.Offset, end))
	} else if opts.Offset > 0 {
		input.Range = aws.String(fmt.Sprintf("bytes=%d-", opts.Offset))
	}

	output, err := s.client.GetObject(ctx, input)
	if err != nil {
		if isAWSNotFound(err) {
			return nil, ObjectInfo{}, ErrNotFound
		}
		return nil, ObjectInfo{}, err
	}

	info := ObjectInfo{
		Key:          key,
		Size:         aws.ToInt64(output.ContentLength),
		LastModified: aws.ToTime(output.LastModified),
		StorageClass: string(output.StorageClass),
		ETag:         strings.Trim(aws.ToString(output.ETag), "\""),
		VersionID:    aws.ToString(output.VersionId),
		IsMarker:     strings.HasSuffix(key, "/") && aws.ToInt64(output.ContentLength) == 0,
	}
	return output.Body, info, nil
}

func (s *S3Store) Put(ctx context.Context, bucket, key string, body io.Reader, opts PutOptions) (ObjectInfo, error) {
	input := &s3.PutObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
		Body:   body,
	}
	if opts.ContentType != "" {
		input.ContentType = aws.String(opts.ContentType)
	}
	if opts.StorageClass != "" {
		input.StorageClass = types.StorageClass(opts.StorageClass)
	}
	if len(opts.Metadata) > 0 {
		input.Metadata = opts.Metadata
	}

	output, err := s.client.PutObject(ctx, input)
	if err != nil {
		return ObjectInfo{}, err
	}

	head, err := s.Head(ctx, bucket, key)
	if err != nil {
		return ObjectInfo{
			Key:       key,
			ETag:      strings.Trim(aws.ToString(output.ETag), "\""),
			VersionID: aws.ToString(output.VersionId),
		}, nil
	}
	return head, nil
}

func (s *S3Store) Copy(ctx context.Context, srcBucket, srcKey, dstBucket, dstKey string, opts CopyOptions) (ObjectInfo, error) {
	input := &s3.CopyObjectInput{
		Bucket:     aws.String(dstBucket),
		Key:        aws.String(dstKey),
		CopySource: aws.String(url.PathEscape(srcBucket + "/" + srcKey)),
	}
	if len(opts.Metadata) > 0 {
		input.Metadata = opts.Metadata
		input.MetadataDirective = types.MetadataDirectiveReplace
	} else if opts.PreserveMetadata {
		input.MetadataDirective = types.MetadataDirectiveCopy
	}

	if _, err := s.client.CopyObject(ctx, input); err != nil {
		if isAWSNotFound(err) {
			return ObjectInfo{}, ErrNotFound
		}
		return ObjectInfo{}, err
	}
	return s.Head(ctx, dstBucket, dstKey)
}

func (s *S3Store) Delete(ctx context.Context, bucket, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil && isAWSNotFound(err) {
		return ErrNotFound
	}
	return err
}

func (s *S3Store) MultipartCopy(ctx context.Context, srcBucket, srcKey, dstBucket, dstKey string, partSize int64, opts CopyOptions) (ObjectInfo, error) {
	if partSize <= 0 {
		partSize = 5 * 1024 * 1024
	}

	createInput := &s3.CreateMultipartUploadInput{
		Bucket: aws.String(dstBucket),
		Key:    aws.String(dstKey),
	}
	if len(opts.Metadata) > 0 {
		createInput.Metadata = opts.Metadata
	}

	upload, err := s.client.CreateMultipartUpload(ctx, createInput)
	if err != nil {
		return ObjectInfo{}, err
	}

	head, err := s.Head(ctx, srcBucket, srcKey)
	if err != nil {
		return ObjectInfo{}, err
	}

	var completed []types.CompletedPart
	for partNum, offset := int32(1), int64(0); offset < head.Size; partNum, offset = partNum+1, offset+partSize {
		end := offset + partSize - 1
		if end >= head.Size {
			end = head.Size - 1
		}
		resp, partErr := s.client.UploadPartCopy(ctx, &s3.UploadPartCopyInput{
			Bucket:          aws.String(dstBucket),
			Key:             aws.String(dstKey),
			UploadId:        upload.UploadId,
			PartNumber:      aws.Int32(partNum),
			CopySource:      aws.String(url.PathEscape(srcBucket + "/" + srcKey)),
			CopySourceRange: aws.String(fmt.Sprintf("bytes=%d-%d", offset, end)),
		})
		if partErr != nil {
			_, _ = s.client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
				Bucket:   aws.String(dstBucket),
				Key:      aws.String(dstKey),
				UploadId: upload.UploadId,
			})
			return ObjectInfo{}, partErr
		}
		completed = append(completed, types.CompletedPart{
			ETag:       resp.CopyPartResult.ETag,
			PartNumber: aws.Int32(partNum),
		})
	}

	_, err = s.client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:   aws.String(dstBucket),
		Key:      aws.String(dstKey),
		UploadId: upload.UploadId,
		MultipartUpload: &types.CompletedMultipartUpload{
			Parts: completed,
		},
	})
	if err != nil {
		return ObjectInfo{}, err
	}

	return s.Head(ctx, dstBucket, dstKey)
}

func (s *S3Store) MultipartUpload(ctx context.Context, bucket, key string, body io.Reader, partSize int64, opts PutOptions) (ObjectInfo, error) {
	uploader := manager.NewUploader(s.client, func(u *manager.Uploader) {
		if partSize > 0 {
			u.PartSize = partSize
		}
	})

	input := &s3.PutObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
		Body:   body,
	}
	if opts.ContentType != "" {
		input.ContentType = aws.String(opts.ContentType)
	}
	if opts.StorageClass != "" {
		input.StorageClass = types.StorageClass(opts.StorageClass)
	}
	if len(opts.Metadata) > 0 {
		input.Metadata = opts.Metadata
	}

	output, err := uploader.Upload(ctx, input)
	if err != nil {
		return ObjectInfo{}, err
	}

	head, err := s.Head(ctx, bucket, key)
	if err != nil {
		return ObjectInfo{Key: key, VersionID: aws.ToString(output.VersionID)}, nil
	}
	return head, nil
}

func isAWSNotFound(err error) bool {
	if err == nil {
		return false
	}

	var apiErr *awshttp.ResponseError
	if errors.As(err, &apiErr) && apiErr.HTTPStatusCode() == 404 {
		return true
	}

	var noSuchKey *types.NoSuchKey
	if errors.As(err, &noSuchKey) {
		return true
	}

	var notFound *types.NotFound
	if errors.As(err, &notFound) {
		return true
	}

	var noSuchBucket *types.NoSuchBucket
	if errors.As(err, &noSuchBucket) {
		return true
	}

	return strings.Contains(strings.ToLower(err.Error()), "not found")
}
