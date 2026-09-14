package abuse

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLimiterBoundsRefillAndConcurrency(t *testing.T) {
	l := NewLimiter(2)
	now := time.Now()
	l.now = func() time.Time { return now }
	for i := 0; i < 10; i++ {
		if ok, _ := l.Allow("a", 10); !ok {
			t.Fatal("early limit")
		}
	}
	if ok, retry := l.Allow("a", 10); ok || retry != 6 {
		t.Fatal("missing retry", ok, retry)
	}
	if ok, _ := l.Allow("b", 10); !ok {
		t.Fatal("independent key blocked")
	}
	if ok, _ := l.Allow("c", 10); ok || len(l.entries) != 2 {
		t.Fatal("unbounded storage")
	}
	if ok, _ := l.Allow("a", 10); ok {
		t.Fatal("capacity pressure reset throttled key")
	}
	now = now.Add(6 * time.Second)
	if ok, _ := l.Allow("a", 10); !ok {
		t.Fatal("no refill")
	}
	now = now.Add(11 * time.Minute)
	if ok, _ := l.Allow("c", 10); !ok || len(l.entries) != 1 {
		t.Fatal("no stale eviction")
	}
	l = NewLimiter(100)
	l.now = func() time.Time { return now }
	var passed atomic.Int64
	var wg sync.WaitGroup
	for range 100 {
		wg.Go(func() {
			if ok, _ := l.Allow("concurrent", 10); ok {
				passed.Add(1)
			}
		})
	}
	wg.Wait()
	if passed.Load() != 10 {
		t.Fatal("raced limit", passed.Load())
	}
}

func TestClientIP(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("fd00::/8")}
	for _, tt := range []struct {
		peer, xff, want string
		bad             bool
	}{
		{"203.0.113.1:10", "1.1.1.1", "203.0.113.1", false},
		{"10.0.0.1:10", "198.51.100.5", "198.51.100.5", false},
		{"10.0.0.1:10", "1.1.1.1, 198.51.100.5, 10.0.0.2", "198.51.100.5", false},
		{"[fd00::1]:10", "2001:db8::5, fd00::2", "2001:db8::5", false},
		{"10.0.0.1:10", "bad, 198.51.100.5", "10.0.0.1", true},
		{"10.0.0.1:10", "", "10.0.0.1", true},
		{"10.0.0.1:10", "198.51.100.5:99", "10.0.0.1", true},
		{"[::ffff:203.0.113.1]:10", "1.1.1.1", "203.0.113.1", false},
	} {
		t.Run(tt.peer+tt.xff, func(t *testing.T) {
			r := httptest.NewRequest("GET", "http://test/", nil)
			r.RemoteAddr = tt.peer
			r.Header.Set("X-Forwarded-For", tt.xff)
			r.Header.Set("CF-Connecting-IP", "9.9.9.9")
			r.Header.Set("X-Real-IP", "9.9.9.9")
			got, bad := ClientIP(r, trusted)
			if got.String() != tt.want || bad != tt.bad {
				t.Fatal(got, bad)
			}
			got, _ = ClientIP(r, nil)
			host := strings.TrimSuffix(tt.peer, ":10")
			host = strings.Trim(host, "[]")
			if got != netip.MustParseAddr(host).Unmap() {
				t.Fatal("trusted by default")
			}
		})
	}
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestSiteverifyDefensiveTransportAndValidation(t *testing.T) {
	for _, tt := range []struct {
		name, body string
		status     int
		want       error
	}{
		{"valid", `{"success":true,"hostname":"photos.test","action":"photodrop_upload"}`, 200, nil},
		{"failed", `{"success":false}`, 200, ErrChallenge},
		{"replayed", `{"success":false,"error-codes":["timeout-or-duplicate"]}`, 200, ErrChallengeExpired},
		{"wrong host", `{"success":true,"hostname":"evil.test","action":"photodrop_upload"}`, 200, ErrChallenge},
		{"wrong action", `{"success":true,"hostname":"photos.test","action":"other"}`, 200, ErrChallenge},
		{"missing fields", `{"success":true}`, 200, ErrChallenge},
		{"malformed", `{"success":`, 200, ErrChallengeUnavailable},
		{"trailing", `{} {}`, 200, ErrChallengeUnavailable},
		{"oversize", strings.Repeat("x", 16385), 200, ErrChallengeUnavailable},
		{"http failure", `{}`, 503, ErrChallengeUnavailable},
		{"redirect", `{}`, 302, ErrChallengeUnavailable},
	} {
		t.Run(tt.name, func(t *testing.T) {
			v := NewTurnstile("private-secret")
			v.client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.String() != siteverifyURL || r.URL.Scheme != "https" {
					t.Fatal("wrong endpoint")
				}
				if err := r.ParseForm(); err != nil || r.Form.Get("secret") != "private-secret" || r.Form.Get("response") != "private-token" {
					t.Fatal("bad body")
				}
				return &http.Response{StatusCode: tt.status, Body: io.NopCloser(strings.NewReader(tt.body)), Header: http.Header{}}, nil
			})
			result, err := v.Verify(t.Context(), "private-token")
			if err == nil {
				err = Validate(result, "photos.test")
			}
			if !errors.Is(err, tt.want) {
				t.Fatalf("got %v want %v", err, tt.want)
			}
			if strings.Contains(fmt.Sprintf("%v %#v", v, v), "private-secret") {
				t.Fatal("secret formatting")
			}
		})
	}
	v := NewTurnstile("secret")
	v.client.Timeout = time.Millisecond
	v.client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, errors.New("private-token private-secret")
	})
	if _, err := v.Verify(context.Background(), "private-token"); err != ErrChallengeUnavailable {
		t.Fatal("timeout not safe", err)
	}
	if _, err := v.Verify(t.Context(), strings.Repeat("x", 2049)); err != ErrChallenge {
		t.Fatal("unbounded token")
	}
}
