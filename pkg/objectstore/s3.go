// SPDX-License-Identifier: MIT

package objectstore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// s3Store writes objects to a configured S3 bucket + prefix. The
// access URL returned by Put is the canonical s3:// form so a
// downstream renderer can presign or re-fetch the object.
type s3Store struct {
	client *s3.Client
	bucket string
	prefix string
}

func newS3Store(u *url.URL) (*s3Store, error) {
	bucket := u.Host
	if bucket == "" {
		return nil, fmt.Errorf("objectstore: s3:// requires a bucket (host)")
	}
	prefix := strings.TrimPrefix(u.Path, "/")
	cfg, err := awsconfig.LoadDefaultConfig(context.Background())
	if err != nil {
		return nil, fmt.Errorf("objectstore: s3 LoadDefaultConfig: %w", err)
	}
	// AWS_ENDPOINT_URL (used by LocalStack et al.) is honoured by the
	// SDK's BaseEndpoint resolver when supplied via env.
	return &s3Store{
		client: s3.NewFromConfig(cfg),
		bucket: bucket,
		prefix: prefix,
	}, nil
}

func (s *s3Store) Put(ctx context.Context, key string, body io.Reader, contentType string) (string, error) {
	full := joinKey(s.prefix, key)
	buf, err := io.ReadAll(body)
	if err != nil {
		return "", err
	}
	input := &s3.PutObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(full),
		Body:   bytes.NewReader(buf),
	}
	if contentType != "" {
		input.ContentType = aws.String(contentType)
	}
	if _, err := s.client.PutObject(ctx, input); err != nil {
		return "", fmt.Errorf("objectstore: s3 PutObject: %w", err)
	}
	return fmt.Sprintf("s3://%s/%s", s.bucket, full), nil
}

func (s *s3Store) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(joinKey(s.prefix, key)),
	})
	if err != nil {
		var nsk *types.NoSuchKey
		if errors.As(err, &nsk) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("objectstore: s3 GetObject: %w", err)
	}
	return out.Body, nil
}

func (s *s3Store) Close() error { return nil }
