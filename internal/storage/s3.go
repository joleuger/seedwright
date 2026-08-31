package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// S3Client wraps the AWS SDK v2 S3 client.
type S3Client struct {
	client *s3.Client
	bucket string
}

// NewS3Storage creates a new S3 client from the given configuration.
func NewS3Storage(endpoint, region, bucket, accessKey, secretKey string, forcePathStyle bool) (*S3Client, error) {
	creds := credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")

	cfg, err := config.LoadDefaultConfig(context.Background(),
	        config.WithResponseChecksumValidation(aws.ResponseChecksumValidationWhenRequired),
		config.WithCredentialsProvider(creds),
		config.WithRegion(region),
	)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	svc := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = &endpoint
		o.UsePathStyle = forcePathStyle
	})

	return &S3Client{
		client: svc,
		bucket: bucket,
	}, nil
}

func (c *S3Client) ListObjects(ctx context.Context, prefix string) ([]ObjectInfo, error) {
	p := prefix
	if p != "" && p[len(p)-1] != '/' {
		p += "/"
	}

	var items []ObjectInfo
	paginator := s3.NewListObjectsV2Paginator(c.client, &s3.ListObjectsV2Input{
		Bucket:  aws.String(c.bucket),
		Prefix:  aws.String(p),
		MaxKeys: aws.Int32(1000),
	})

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list objects page: %w", err)
		}
		for _, obj := range page.Contents {
			items = append(items, ObjectInfo{
				Key:          aws.ToString(obj.Key),
				Size:         ptrInt64(obj.Size),
				LastModified: *obj.LastModified,
				ETag:         strings.Trim(aws.ToString(obj.ETag), `"`),
			})
		}
	}

	return items, nil
}

func (c *S3Client) GetObject(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	result, err := c.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, 0, fmt.Errorf("get object %s: %w", key, err)
	}

	size := ptrInt64(result.ContentLength)
	return result.Body, size, nil
}

// LocalFile downloads the object into a temp file and returns its path
// plus a cleanup that removes it. The caller MUST call cleanup when it is
// non-nil; the path is valid only until then.
func (c *S3Client) LocalFile(ctx context.Context, key string) (string, func(), error) {
	result, err := c.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return "", nil, fmt.Errorf("get object %s: %w", key, err)
	}
	defer result.Body.Close()

	f, err := os.CreateTemp("", "sdcpp-s3-")
	if err != nil {
		return "", nil, fmt.Errorf("create temp file for %s: %w", key, err)
	}
	name := f.Name()
	if _, err := io.Copy(f, result.Body); err != nil {
		f.Close()
		os.Remove(name)
		return "", nil, fmt.Errorf("write temp file for %s: %w", key, err)
	}
	if err := f.Close(); err != nil {
		os.Remove(name)
		return "", nil, fmt.Errorf("close temp file for %s: %w", key, err)
	}

	cleanup := func() { os.Remove(name) }
	return name, cleanup, nil
}

func (c *S3Client) PutObject(ctx context.Context, key string, data io.Reader, size int64, contentType string) error {
	// AWS SDK v2 needs to seek the body to compute the content header checksum.
	// With non-TLS endpoints, streaming checksums are unavailable, so we buffer
	// the stream into memory (bytes.Reader implements io.Seeker).
	var body io.Reader = data

	// If the reader is already seekable, use it directly.
	if _, isSeeker := data.(io.Seeker); !isSeeker {
		buf, err := io.ReadAll(data)
		if err != nil {
			return fmt.Errorf("read body for put object %s: %w", key, err)
		}
		body = bytes.NewReader(buf)
	}

	var length *int64
	if size > 0 {
		length = aws.Int64(size)
	}

	_, err := c.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(c.bucket),
		Key:           aws.String(key),
		Body:          body,
		ContentLength: length,
		ContentType:   aws.String(contentType),
	})
	if err != nil {
		return fmt.Errorf("put object %s: %w", key, err)
	}

	return nil
}

func (c *S3Client) DeleteObject(ctx context.Context, key string) error {
	_, err := c.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("delete object %s: %w", key, err)
	}
	return nil
}

func (c *S3Client) PresignedURLsSupported() bool { return true }

func (c *S3Client) PresignedGetObject(ctx context.Context, key string, expiry time.Duration) (string, error) {
	presign := s3.NewPresignClient(c.client)
	result, err := presign.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	}, func(o *s3.PresignOptions) {
		o.Expires = expiry
	})
	if err != nil {
		return "", fmt.Errorf("presign get object %s: %w", key, err)
	}

	return result.URL, nil
}

// ptrInt64 safely converts a *int64 to int64, returning 0 for nil.
func ptrInt64(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}
