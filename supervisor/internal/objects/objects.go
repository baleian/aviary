// Package objects keeps the files that messages carry in the object storage
// of the installation, the one that Agent Substrate keeps its snapshots in,
// through its S3 API.
package objects

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

var (
	// ErrNotFound means that the key has no object.
	ErrNotFound = errors.New("no such object")
	// ErrFull means that the object storage has no room for the object.
	ErrFull = errors.New("no room for the object")
)

// Store keeps objects under the prefix of a bucket.
type Store struct {
	client         *s3.Client
	bucket, prefix string
}

// Open returns the store at location, such as s3://bucket/ or
// s3://bucket/prefix/. Its client reads the standard AWS variables, such as
// AWS_REGION, AWS_ENDPOINT_URL and the credentials, and addresses the bucket
// in the path if AWS_S3_USE_PATH_STYLE is true, as Agent Substrate does.
func Open(ctx context.Context, location string) (*Store, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("object storage: %w", err)
	}
	return New(cfg, location, func(o *s3.Options) {
		o.UsePathStyle = os.Getenv("AWS_S3_USE_PATH_STYLE") == "true"
	})
}

// New returns the store at location, with a client of cfg and options.
func New(cfg aws.Config, location string, options ...func(*s3.Options)) (*Store, error) {
	at, err := url.Parse(location)
	if err != nil || at.Scheme != "s3" || at.Host == "" || at.User != nil || at.RawQuery != "" || at.Fragment != "" {
		return nil, fmt.Errorf("object storage: the location must be such as s3://bucket/prefix/, not %q", location)
	}
	prefix := strings.TrimPrefix(at.Path, "/")
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	client := s3.NewFromConfig(cfg, append([]func(*s3.Options){func(o *s3.Options) {
		// Stores other than Amazon S3 may refuse the checksums that the
		// client otherwise adds to every request.
		o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
		o.ResponseChecksumValidation = aws.ResponseChecksumValidationWhenRequired
	}}, options...)...)
	return &Store{client: client, bucket: at.Host, prefix: prefix}, nil
}

// Put writes body, of size bytes, as the object of key. The body is streamed
// in a single request whose signature leaves out the payload, so that it is
// read only once.
func (s *Store) Put(ctx context.Context, key string, body io.Reader, size int64) error {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: &s.bucket, Key: aws.String(s.prefix + key), Body: body, ContentLength: aws.Int64(size),
	}, s3.WithAPIOptions(v4.SwapComputePayloadSHA256ForUnsignedPayloadMiddleware))
	return failure(http.MethodPut, key, err)
}

// Get returns the object of key and its size. The caller closes it. It
// returns ErrNotFound if the key has no object.
func (s *Store) Get(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	object, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: &s.bucket, Key: aws.String(s.prefix + key)})
	if err != nil {
		return nil, 0, failure(http.MethodGet, key, err)
	}
	if object.ContentLength == nil {
		object.Body.Close()
		return nil, 0, fmt.Errorf("object storage: GET %s: the answer has no length", key)
	}
	return object.Body, *object.ContentLength, nil
}

// Delete deletes the object of key and every object whose key is under it.
// A key without objects is not an error.
func (s *Store) Delete(ctx context.Context, key string) error {
	keys := []string{s.prefix + key}
	pages := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{
		Bucket: &s.bucket, Prefix: aws.String(s.prefix + key + "/"),
	})
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		if err != nil {
			return failure("LIST", key, err)
		}
		for _, object := range page.Contents {
			keys = append(keys, aws.ToString(object.Key))
		}
	}
	for _, each := range keys {
		_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &s.bucket, Key: aws.String(each)})
		if err != nil {
			return failure(http.MethodDelete, strings.TrimPrefix(each, s.prefix), err)
		}
	}
	return nil
}

// failure returns the error of a request for key, if any, as ErrNotFound or
// ErrFull if it is one of them.
func failure(method, key string, err error) error {
	if err == nil {
		return nil
	}
	var missing *types.NoSuchKey
	var answer interface{ HTTPStatusCode() int }
	switch {
	case errors.As(err, &missing):
		err = ErrNotFound
	case errors.As(err, &answer) && answer.HTTPStatusCode() == http.StatusInsufficientStorage:
		err = ErrFull
	}
	return fmt.Errorf("object storage: %s %s: %w", method, key, err)
}
