package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"github.com/aws/smithy-go/logging"
	"github.com/aws/smithy-go/middleware"
	"photodrop/internal/config"
)

type S3 struct {
	client  *s3.Client
	presign *s3.PresignClient
	cfg     config.S3
	target  string
}

func NewS3(cfg config.S3) *S3 {
	client := s3.NewFromConfig(aws.Config{
		Region: cfg.Region, Credentials: credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, cfg.SessionToken),
		HTTPClient:                 &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		Logger:                     logging.NewStandardLogger(io.Discard),
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
	}, func(o *s3.Options) {
		o.UsePathStyle = cfg.PathStyle
		o.RetryMaxAttempts = 2
		if cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		}
	})
	// Preserve Gate 4's fingerprint for legacy association and compatibility
	// metadata. Runtime routing is authoritative through storage_backend_id.
	digest := sha256.Sum256([]byte(cfg.Endpoint + "\n" + cfg.Region + "\n" + cfg.Bucket + "\n" + cfg.Prefix))
	return &S3{client: client, presign: s3.NewPresignClient(client), cfg: cfg, target: hex.EncodeToString(digest[:])}
}
func (s *S3) Target() string { return s.target }
func (s *S3) Key(eventID int64, assetID string) string {
	return s.cfg.Prefix + "events/e" + strconv.FormatInt(eventID, 10) + "/assets/" + assetID
}

var s3Suffix = regexp.MustCompile(`^events/e[1-9][0-9]{0,18}/assets/[0-9a-f]{32}$`)

func (s *S3) validKey(key string) bool {
	return strings.HasPrefix(key, s.cfg.Prefix) && s3Suffix.MatchString(strings.TrimPrefix(key, s.cfg.Prefix))
}

func (s *S3) Authorize(ctx context.Context, key, kind string) (UploadPlan, error) {
	if !s.validKey(key) {
		return UploadPlan{}, ErrInvalidKey
	}
	now := time.Now().UTC()
	result, err := s.presign.PresignPutObject(ctx, &s3.PutObjectInput{Bucket: &s.cfg.Bucket, Key: &key, ContentType: &kind, IfNoneMatch: aws.String("*")}, func(o *s3.PresignOptions) {
		o.Expires = s.cfg.PresignTTL
		o.ClientOptions = append(o.ClientOptions, func(options *s3.Options) {
			options.APIOptions = append(options.APIOptions, func(stack *middleware.Stack) error {
				// The SDK otherwise removes Content-Type when presigning a bodyless PUT.
				_, err := stack.Build.Remove("RemoveContentTypeHeader")
				return err
			})
		})
	})
	if err != nil {
		return UploadPlan{}, safeS3Error(err)
	}
	if result.SignedHeader.Get("Content-Type") != kind || result.SignedHeader.Get("If-None-Match") != "*" {
		return UploadPlan{}, ErrUnavailable
	}
	headers := map[string]string{}
	for name, values := range result.SignedHeader {
		if !strings.EqualFold(name, "Host") {
			headers[name] = strings.Join(values, ",")
		}
	}
	return UploadPlan{"direct", result.Method, result.URL, headers, now.Add(s.cfg.PresignTTL)}, nil
}

// UploadOrigin derives the exact CSP origin using the SDK endpoint resolver.
// The probe is only locally signed; it performs no network operation.
func (s *S3) UploadOrigin(ctx context.Context) (string, error) {
	p, err := s.Authorize(ctx, s.Key(1, strings.Repeat("0", 32)), "image/png")
	if err != nil {
		return "", err
	}
	u, err := url.Parse(p.URL)
	if err != nil {
		return "", ErrUnavailable
	}
	return u.Scheme + "://" + u.Host, nil
}
func (s *S3) Head(ctx context.Context, key string) (ObjectInfo, error) {
	if !s.validKey(key) {
		return ObjectInfo{}, ErrInvalidKey
	}
	o, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: &s.cfg.Bucket, Key: &key})
	if err != nil {
		return ObjectInfo{}, safeS3Error(err)
	}
	return ObjectInfo{aws.ToInt64(o.ContentLength), aws.ToString(o.ContentType), aws.ToString(o.ETag)}, nil
}
func (s *S3) ReadPrefix(ctx context.Context, key string, info ObjectInfo) ([]byte, error) {
	if !s.validKey(key) {
		return nil, ErrInvalidKey
	}
	if info.Size <= 0 || info.ETag == "" {
		return nil, ErrObjectChanged
	}
	n := min(info.Size, 512)
	o, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: &s.cfg.Bucket, Key: &key, Range: aws.String("bytes=0-511"), IfMatch: &info.ETag})
	if err != nil {
		return nil, safeS3Error(err)
	}
	defer o.Body.Close()
	if aws.ToString(o.ContentRange) != fmt.Sprintf("bytes 0-%d/%d", n-1, info.Size) || aws.ToInt64(o.ContentLength) != n || aws.ToString(o.ETag) != info.ETag {
		return nil, ErrObjectChanged
	}
	data, err := io.ReadAll(io.LimitReader(o.Body, n+1))
	if err != nil {
		return nil, ErrUnavailable
	}
	if int64(len(data)) != n {
		return nil, ErrObjectChanged
	}
	return data, nil
}
func (s *S3) Delete(ctx context.Context, key string) error {
	if !s.validKey(key) {
		return ErrInvalidKey
	}
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &s.cfg.Bucket, Key: &key})
	if err == nil {
		return nil
	}
	err = safeS3Error(err)
	if errors.Is(err, ErrMissing) {
		return nil
	}
	return err
}

// Never wrap SDK errors: transport/provider errors may contain credentials,
// signed query parameters, endpoints, object keys, or arbitrary response text.
func safeS3Error(err error) error {
	var api smithy.APIError
	if errors.As(err, &api) {
		switch api.ErrorCode() {
		case "NoSuchKey", "NotFound", "NoSuchObject":
			return ErrMissing
		case "PreconditionFailed":
			return ErrObjectChanged
		}
	}
	return ErrUnavailable
}
