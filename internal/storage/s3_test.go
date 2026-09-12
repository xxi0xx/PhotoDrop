package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"photodrop/internal/config"
	"photodrop/internal/testutil"
	"photodrop/internal/testutil/s3test"
	"strings"
	"testing"
	"time"
)

func sdkFixture(t *testing.T) (*S3, *s3test.Server) {
	t.Helper()
	fake := s3test.New("https://photos.test")
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	return NewS3(config.S3{Bucket: "photos", Region: s3test.Region, Endpoint: server.URL, AccessKeyID: s3test.AccessKey, SecretAccessKey: s3test.SecretKey, PathStyle: true, Prefix: "test/", PresignTTL: 10 * time.Minute}), fake
}
func putPlan(t *testing.T, p UploadPlan, data []byte, status int) {
	t.Helper()
	r, _ := http.NewRequest(p.Method, p.URL, bytes.NewReader(data))
	for k, v := range p.Headers {
		r.Header.Set(k, v)
	}
	response, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != status {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
		t.Fatalf("PUT returned %d: %s", response.StatusCode, body)
	}
}
func TestS3SDKPresignVerifyAndReplay(t *testing.T) {
	s, fake := sdkFixture(t)
	key := s.Key(7, strings.Repeat("a", 32))
	data := testutil.Images()["image/png"]
	p, err := s.Authorize(t.Context(), key, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(p.URL)
	if u.Path != "/photos/"+key || !strings.Contains(u.Query().Get("X-Amz-SignedHeaders"), "content-type") || !strings.Contains(u.Query().Get("X-Amz-SignedHeaders"), "if-none-match") || p.Headers["If-None-Match"] != "*" {
		t.Fatal("authorization not constrained")
	}
	if strings.Contains(fmt.Sprintf("%+v %#v", p, p), "X-Amz-") {
		t.Fatal("plan formatting leaked URL")
	}
	putPlan(t, p, data, 200)
	putPlan(t, p, []byte("overwrite"), 412)
	info, err := s.Head(t.Context(), key)
	if err != nil || info.Size != int64(len(data)) {
		t.Fatal("HEAD verification", info, err)
	}
	prefix, err := s.ReadPrefix(t.Context(), key, info)
	if err != nil || !bytes.Equal(prefix, data) {
		t.Fatal("ranged validation", err)
	}
	fake.IgnoreRange(true)
	if _, err = s.ReadPrefix(t.Context(), key, info); !errors.Is(err, ErrObjectChanged) {
		t.Fatal("accepted ignored Range", err)
	}
	fake.IgnoreRange(false)
	fake.Seed("/photos/"+key, []byte("replacement"), "image/png")
	if _, err = s.ReadPrefix(t.Context(), key, info); !errors.Is(err, ErrObjectChanged) {
		t.Fatal("ignored ETag change", err)
	}
	for _, method := range []string{"HEAD", "GET", "DELETE"} {
		fake.Fault(method, 503)
	}
	if _, err = s.Head(t.Context(), key); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if _, err = s.ReadPrefix(t.Context(), key, info); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if err = s.Delete(t.Context(), key); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	for _, method := range []string{"HEAD", "GET", "DELETE"} {
		fake.Fault(method, 0)
	}
	if err = s.Delete(t.Context(), key); err != nil {
		t.Fatal(err)
	}
	if err = s.Delete(t.Context(), key); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Head(t.Context(), key); !errors.Is(err, ErrMissing) {
		t.Fatal("missing object", err)
	}
	for _, bad := range []string{"../escape", "test/events/e7/assets/../../else", s.Key(7, strings.Repeat("a", 31)), "/" + key, "other/" + key} {
		if _, err = s.Authorize(t.Context(), bad, "image/png"); !errors.Is(err, ErrInvalidKey) {
			t.Fatal("invalid key authorized")
		}
		if err = s.Delete(t.Context(), bad); !errors.Is(err, ErrInvalidKey) {
			t.Fatal("invalid key deleted")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err = s.Head(ctx, key); err == nil {
		t.Fatal("ignored cancellation")
	}
}
