// Package s3test is a deterministic, private S3 test double. It is never linked
// into PhotoDrop's production binary. Signature verification uses the AWS SDK.
package s3test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/signer/v4"
)

const AccessKey = "test-s3-access"
const SecretKey = "test-s3-secret-for-local-tests-only"
const Region = "auto"

type Object struct {
	Data       []byte
	Type, ETag string
}
type Request struct {
	Method, Path, Range string
	Bytes               int
}
type Server struct {
	mu          sync.Mutex
	objects     map[string]Object
	requests    []Request
	faults      map[string]int
	drops       int
	delay       time.Duration
	ignoreRange bool
	Origin      string // fixed before serving
}

func New(origin string) *Server {
	return &Server{objects: map[string]Object{}, faults: map[string]int{}, Origin: origin}
}
func (s *Server) Fault(method string, status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.faults[method] = status
}
func (s *Server) DropPUTResponses(n int) { s.mu.Lock(); defer s.mu.Unlock(); s.drops = n }
func (s *Server) Delay(d time.Duration)  { s.mu.Lock(); defer s.mu.Unlock(); s.delay = d }
func (s *Server) IgnoreRange(v bool)     { s.mu.Lock(); defer s.mu.Unlock(); s.ignoreRange = v }
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.requests...)
}
func (s *Server) Objects() map[string]Object {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]Object{}
	for k, v := range s.objects {
		v.Data = bytes.Clone(v.Data)
		out[k] = v
	}
	return out
}
func (s *Server) Seed(path string, data []byte, kind string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[path] = object(data, kind)
}
func object(data []byte, kind string) Object {
	h := sha256.Sum256(data)
	return Object{bytes.Clone(data), kind, `"` + hex.EncodeToString(h[:]) + `"`}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/__test/requests" && r.Method == "GET" {
		json.NewEncoder(w).Encode(s.Requests())
		return
	}
	if r.Header.Get("Origin") == s.Origin && s.Origin != "" {
		w.Header().Set("Access-Control-Allow-Origin", s.Origin)
		w.Header().Set("Vary", "Origin")
		w.Header().Set("Access-Control-Allow-Methods", "PUT")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, If-None-Match")
		w.Header().Set("Access-Control-Expose-Headers", "ETag")
	}
	if r.Method == "OPTIONS" {
		w.WriteHeader(204)
		return
	}
	if !validSignature(r) {
		s.fail(w, 403, "SignatureDoesNotMatch")
		return
	}
	s.mu.Lock()
	fault := s.faults[r.Method]
	delay := s.delay
	ignoreRange := s.ignoreRange
	s.mu.Unlock()
	if fault != 0 {
		s.fail(w, fault, "InjectedProviderError")
		return
	}
	var data []byte
	if r.Method == "PUT" {
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-r.Context().Done():
				return
			}
		}
		var err error
		data, err = io.ReadAll(io.LimitReader(r.Body, 2*1024*1024+1))
		if err != nil || len(data) > 2*1024*1024 {
			s.fail(w, 400, "InvalidRequest")
			return
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, Request{r.Method, r.URL.Path, r.Header.Get("Range"), len(data)})
	o, exists := s.objects[r.URL.Path]
	switch r.Method {
	case "PUT":
		if r.Header.Get("If-None-Match") != "*" {
			s.fail(w, 400, "MissingConditionalWrite")
			return
		}
		if exists {
			s.fail(w, 412, "PreconditionFailed")
			return
		}
		o = object(data, r.Header.Get("Content-Type"))
		s.objects[r.URL.Path] = o
		if s.drops > 0 {
			s.drops--
			if hijacker, ok := w.(http.Hijacker); ok {
				conn, _, err := hijacker.Hijack()
				if err == nil {
					conn.Close()
					return
				}
			}
		}
		w.Header().Set("ETag", o.ETag)
		w.WriteHeader(200)
	case "HEAD", "GET":
		if !exists {
			s.fail(w, 404, "NoSuchKey")
			return
		}
		if match := r.Header.Get("If-Match"); match != "" && match != o.ETag {
			s.fail(w, 412, "PreconditionFailed")
			return
		}
		w.Header().Set("ETag", o.ETag)
		w.Header().Set("Content-Type", o.Type)
		if r.Method == "HEAD" {
			w.Header().Set("Content-Length", strconv.Itoa(len(o.Data)))
			return
		}
		if !ignoreRange && r.Header.Get("Range") == "bytes=0-511" && len(o.Data) > 0 {
			n := min(512, len(o.Data))
			w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-%d/%d", n-1, len(o.Data)))
			w.Header().Set("Content-Length", strconv.Itoa(n))
			w.WriteHeader(206)
			w.Write(o.Data[:n])
		} else {
			w.Header().Set("Content-Length", strconv.Itoa(len(o.Data)))
			w.Write(o.Data)
		}
	case "DELETE":
		delete(s.objects, r.URL.Path)
		w.WriteHeader(204)
	default:
		s.fail(w, 405, "MethodNotAllowed")
	}
}
func (s *Server) fail(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	fmt.Fprintf(w, "<Error><Code>%s</Code><Message>test provider failure</Message></Error>", code)
}

func validSignature(r *http.Request) bool {
	query := r.URL.Query()
	presigned := query.Get("X-Amz-Signature") != ""
	signedHeaders, signDate, credential, expected := query.Get("X-Amz-SignedHeaders"), query.Get("X-Amz-Date"), query.Get("X-Amz-Credential"), query.Get("X-Amz-Signature")
	if !presigned {
		auth := strings.TrimPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ")
		fields := map[string]string{}
		for _, part := range strings.Split(auth, ", ") {
			k, v, ok := strings.Cut(part, "=")
			if ok {
				fields[k] = v
			}
		}
		signedHeaders = fields["SignedHeaders"]
		credential = fields["Credential"]
		expected = fields["Signature"]
		signDate = r.Header.Get("X-Amz-Date")
	}
	parts := strings.Split(credential, "/")
	if len(parts) != 5 || parts[0] != AccessKey || parts[2] != Region || parts[3] != "s3" {
		return false
	}
	date, err := time.Parse("20060102T150405Z", signDate)
	if err != nil {
		return false
	}
	if presigned {
		ttl, err := strconv.Atoi(query.Get("X-Amz-Expires"))
		if err != nil || time.Now().After(date.Add(time.Duration(ttl)*time.Second)) || date.After(time.Now().Add(time.Minute)) {
			return false
		}
	}
	query.Del("X-Amz-Signature")
	u := url.URL{Scheme: "http", Host: r.Host, Path: r.URL.Path, RawPath: r.URL.RawPath, RawQuery: query.Encode()}
	clone, err := http.NewRequest(r.Method, u.String(), nil)
	if err != nil {
		return false
	}
	if strings.Contains(signedHeaders, "content-length") {
		clone.ContentLength = r.ContentLength
	}
	for _, key := range strings.Split(signedHeaders, ";") {
		if key != "host" && key != "content-length" {
			clone.Header[http.CanonicalHeaderKey(key)] = r.Header.Values(key)
		}
	}
	creds := aws.Credentials{AccessKeyID: AccessKey, SecretAccessKey: SecretKey}
	signer := v4.NewSigner()
	var actual string
	if presigned {
		signed, _, err := signer.PresignHTTP(context.Background(), creds, clone, "UNSIGNED-PAYLOAD", "s3", Region, date)
		if err != nil {
			return false
		}
		u, err := url.Parse(signed)
		if err != nil {
			return false
		}
		actual = u.Query().Get("X-Amz-Signature")
	} else {
		hash := r.Header.Get("X-Amz-Content-Sha256")
		if hash == "" {
			h := sha256.Sum256(nil)
			hash = hex.EncodeToString(h[:])
		}
		if signer.SignHTTP(context.Background(), creds, clone, hash, "s3", Region, date) != nil {
			return false
		}
		_, actual, _ = strings.Cut(clone.Header.Get("Authorization"), "Signature=")
	}
	return expected != "" && subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) == 1
}
