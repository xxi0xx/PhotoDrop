package storage

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

var ErrUnavailable = errors.New("object storage is unavailable; retry later")
var ErrMissing = errors.New("uploaded object was not found")
var ErrObjectChanged = errors.New("uploaded object changed during verification")
var ErrBackend = errors.New("the original S3 configuration is required to manage this asset")

type UploadPlan struct {
	Strategy  string            `json:"strategy"`
	Method    string            `json:"method"`
	URL       string            `json:"url"`
	Headers   map[string]string `json:"headers"`
	ExpiresAt time.Time         `json:"expires_at"`
}

// Plans are bearer credentials. Accidental structured/text logging is redacted.
func (UploadPlan) String() string         { return "direct upload authorization (redacted)" }
func (p UploadPlan) GoString() string     { return p.String() }
func (p UploadPlan) LogValue() slog.Value { return slog.StringValue(p.String()) }

type ObjectInfo struct {
	Size              int64
	ContentType, ETag string
}

// Direct models browser-mediated storage without accepting a media reader.
type Direct interface {
	Target() string
	Key(eventID int64, assetID string) string
	Authorize(context.Context, string, string) (UploadPlan, error)
	Head(context.Context, string) (ObjectInfo, error)
	ReadPrefix(context.Context, string, ObjectInfo) ([]byte, error)
	Delete(context.Context, string) error
}
