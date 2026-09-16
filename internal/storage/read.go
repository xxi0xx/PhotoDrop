package storage

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// Reader is the streaming capability used by export and integrations. It does
// not change the write-only contract of a direct browser upload destination.
type Reader interface {
	Open(context.Context, string) (io.ReadCloser, ObjectInfo, error)
}

type safeStream struct {
	io.ReadCloser
	ctx context.Context
}

func (r *safeStream) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.ReadCloser.Read(p)
	if err != nil && err != io.EOF {
		return n, ErrUnavailable
	}
	return n, err
}

func (l *Local) Open(ctx context.Context, key string) (io.ReadCloser, ObjectInfo, error) {
	if !ValidKey(key) {
		return nil, ObjectInfo{}, ErrInvalidKey
	}
	if err := ctx.Err(); err != nil {
		return nil, ObjectInfo{}, err
	}
	root, err := os.OpenRoot(l.directory)
	if err != nil {
		return nil, ObjectInfo{}, ErrUnavailable
	}
	defer root.Close()
	info, err := root.Lstat(key)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ObjectInfo{}, ErrMissing
	}
	if err != nil || !info.Mode().IsRegular() {
		return nil, ObjectInfo{}, ErrUnavailable
	}
	f, err := root.Open(key)
	if err != nil {
		return nil, ObjectInfo{}, ErrUnavailable
	}
	actual, err := f.Stat()
	if err != nil || !actual.Mode().IsRegular() || !os.SameFile(info, actual) {
		f.Close()
		return nil, ObjectInfo{}, ErrObjectChanged
	}
	return &safeStream{f, ctx}, ObjectInfo{Size: actual.Size()}, nil
}

func (s *S3) Open(ctx context.Context, key string) (io.ReadCloser, ObjectInfo, error) {
	if !s.validKey(key) {
		return nil, ObjectInfo{}, ErrInvalidKey
	}
	o, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: &s.cfg.Bucket, Key: &key}, func(o *s3.Options) {
		// Control-plane requests keep their short timeout. Streaming a large
		// object gets a bounded transfer deadline and still obeys cancellation.
		o.HTTPClient = &http.Client{Timeout: 10 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	})
	if err != nil {
		return nil, ObjectInfo{}, safeS3Error(err)
	}
	return &safeStream{o.Body, ctx}, ObjectInfo{Size: aws.ToInt64(o.ContentLength), ContentType: aws.ToString(o.ContentType), ETag: aws.ToString(o.ETag)}, nil
}
