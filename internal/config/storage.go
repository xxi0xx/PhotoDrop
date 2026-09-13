package config

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type S3 struct {
	Bucket, Region, Endpoint, Prefix           string
	AccessKeyID, SecretAccessKey, SessionToken string
	PathStyle                                  bool
	PresignTTL                                 time.Duration
}

func (S3) String() string     { return "S3 configuration (credentials redacted)" }
func (s S3) GoString() string { return s.String() }
func (s S3) MarshalJSON() ([]byte, error) {
	return []byte(`"S3 configuration (credentials redacted)"`), nil
}
func (s S3) Configured() bool {
	return s.Bucket != "" && s.AccessKeyID != "" && s.SecretAccessKey != ""
}

var bucketName = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)
var regionName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
var prefixSegment = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// Restrict prefixes to plain path segments. Never silently clean traversal.
func NormalizePrefix(value string) (string, error) {
	value = strings.Trim(value, "/")
	if len(value) > 200 {
		return "", fmt.Errorf("PHOTODROP_S3_PREFIX is too long")
	}
	if value == "" {
		return "", nil
	}
	for _, segment := range strings.Split(value, "/") {
		if !prefixSegment.MatchString(segment) {
			return "", fmt.Errorf("PHOTODROP_S3_PREFIX must contain slash-separated letters, digits, underscores or hyphens")
		}
	}
	return value + "/", nil
}

func (c *Config) loadStorageSingle(lookup func(string) (string, bool)) error {
	c.StorageProvider = "local"
	if v, ok := lookup("PHOTODROP_STORAGE_PROVIDER"); ok {
		c.StorageProvider = v
	}
	if c.StorageProvider != "local" && c.StorageProvider != "s3" {
		return fmt.Errorf("PHOTODROP_STORAGE_PROVIDER must be local or s3")
	}
	c.S3 = S3{Region: "auto", Prefix: "photodrop/", PresignTTL: 10 * time.Minute}
	for name, ptr := range map[string]*string{
		"BUCKET": &c.S3.Bucket, "REGION": &c.S3.Region, "ENDPOINT": &c.S3.Endpoint, "PREFIX": &c.S3.Prefix,
		"ACCESS_KEY_ID": &c.S3.AccessKeyID, "SECRET_ACCESS_KEY": &c.S3.SecretAccessKey, "SESSION_TOKEN": &c.S3.SessionToken,
	} {
		if v, ok := lookup("PHOTODROP_S3_" + name); ok {
			*ptr = v
		}
	}
	if v, ok := lookup("PHOTODROP_S3_PATH_STYLE"); ok {
		if v != "true" && v != "false" {
			return fmt.Errorf("PHOTODROP_S3_PATH_STYLE must be true or false")
		}
		c.S3.PathStyle = v == "true"
	}
	if v, ok := lookup("PHOTODROP_S3_PRESIGN_TTL"); ok {
		d, err := time.ParseDuration(v)
		if err != nil || d < time.Minute || d > 15*time.Minute {
			return fmt.Errorf("PHOTODROP_S3_PRESIGN_TTL must be from 1m to 15m")
		}
		c.S3.PresignTTL = d
	}
	var err error
	if c.S3.Prefix, err = NormalizePrefix(c.S3.Prefix); err != nil {
		return err
	}
	if !regionName.MatchString(c.S3.Region) {
		return fmt.Errorf("PHOTODROP_S3_REGION must be a non-empty region identifier")
	}
	if c.S3.Endpoint != "" {
		u, err := url.Parse(c.S3.Endpoint)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || !validHost(u.Hostname()) || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || strings.Contains(c.S3.Endpoint, "#") || strings.HasSuffix(u.Host, ":") || (u.Port() != "" && !validPort(u.Port())) {
			return fmt.Errorf("PHOTODROP_S3_ENDPOINT must be an http(s) origin without credentials, path, query or fragment")
		}
		u.Host = strings.ToLower(u.Host)
		c.S3.Endpoint = strings.TrimSuffix(u.String(), "/")
	}
	// Retain a configured S3 backend in local mode to manage historical objects.
	needed := c.StorageProvider == "s3" || c.S3.Bucket != "" || c.S3.AccessKeyID != "" || c.S3.SecretAccessKey != "" || c.S3.SessionToken != ""
	if !needed {
		return nil
	}
	if !bucketName.MatchString(c.S3.Bucket) || strings.Contains(c.S3.Bucket, "..") || strings.Contains(c.S3.Bucket, ".-") || strings.Contains(c.S3.Bucket, "-.") || net.ParseIP(c.S3.Bucket) != nil {
		return fmt.Errorf("PHOTODROP_S3_BUCKET must be a valid private bucket name")
	}
	for name, value := range map[string]string{"ACCESS_KEY_ID": c.S3.AccessKeyID, "SECRET_ACCESS_KEY": c.S3.SecretAccessKey} {
		if strings.TrimSpace(value) == "" || strings.ContainsAny(value, "\x00\r\n") {
			return fmt.Errorf("PHOTODROP_S3_%s is required for a configured S3 backend", name)
		}
	}
	if strings.ContainsAny(c.S3.SessionToken, "\x00\r\n") {
		return fmt.Errorf("PHOTODROP_S3_SESSION_TOKEN is invalid")
	}
	return nil
}
