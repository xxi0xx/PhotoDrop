package config

import (
	"fmt"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Security struct {
	SessionTTL                        time.Duration
	SessionMaxAssets, SessionMaxBytes int64
	RateMultiplier                    int
	RateDisabled                      bool
	TrustedProxies                    []netip.Prefix
	TurnstileSiteKey                  string
	TurnstileSecretKey                string `json:"-"`
}

func (Security) String() string     { return "Security configuration (credentials redacted)" }
func (s Security) GoString() string { return s.String() }
func (s Security) Defaults() Security {
	if s.SessionTTL == 0 {
		s.SessionTTL = 2 * time.Hour
	}
	if s.SessionMaxAssets == 0 {
		s.SessionMaxAssets = 100
	}
	if s.SessionMaxBytes == 0 {
		s.SessionMaxBytes = 5 * 1024 * 1024 * 1024
	}
	if s.RateMultiplier == 0 {
		s.RateMultiplier = 1
	}
	return s
}
func (c *Config) loadSecurity(lookup func(string) (string, bool)) error {
	s := Security{}.Defaults()
	for name, dst := range map[string]*string{"SITE_KEY": &s.TurnstileSiteKey, "SECRET_KEY": &s.TurnstileSecretKey} {
		if v, ok := lookup("PHOTODROP_TURNSTILE_" + name); ok {
			*dst = v
		}
		if *dst != "" && (len(*dst) > 256 || strings.TrimSpace(*dst) != *dst || strings.ContainsAny(*dst, "\x00\r\n\t ")) {
			return fmt.Errorf("PHOTODROP_TURNSTILE_%s must be a valid key", name)
		}
	}
	if (s.TurnstileSiteKey == "") != (s.TurnstileSecretKey == "") {
		return fmt.Errorf("set both PHOTODROP_TURNSTILE_SITE_KEY and PHOTODROP_TURNSTILE_SECRET_KEY, or neither")
	}
	if s.TurnstileSiteKey != "" {
		u, err := url.Parse(c.BaseURL)
		if err != nil || u.Hostname() == "" {
			return fmt.Errorf("PHOTODROP_BASE_URL is required when Turnstile is enabled")
		}
	}
	if v, ok := lookup("PHOTODROP_UPLOAD_SESSION_TTL"); ok && v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < time.Minute || d > 24*time.Hour {
			return fmt.Errorf("PHOTODROP_UPLOAD_SESSION_TTL must be between 1m and 24h")
		}
		s.SessionTTL = d
	}
	for name, item := range map[string]struct {
		dst *int64
		max int64
	}{
		"PHOTODROP_UPLOAD_SESSION_MAX_ASSETS": {&s.SessionMaxAssets, 1000},
		"PHOTODROP_UPLOAD_SESSION_MAX_BYTES":  {&s.SessionMaxBytes, 1 << 40},
	} {
		if v, ok := lookup(name); ok && v != "" {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n < 1 || n > item.max || strings.Trim(v, "0123456789") != "" {
				return fmt.Errorf("%s must be an integer from 1 to %d", name, item.max)
			}
			*item.dst = n
		}
	}
	if v, ok := lookup("PHOTODROP_RATE_LIMIT_MULTIPLIER"); ok && v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 100 {
			return fmt.Errorf("PHOTODROP_RATE_LIMIT_MULTIPLIER must be from 1 to 100")
		}
		s.RateMultiplier = n
	}
	if v, ok := lookup("PHOTODROP_RATE_LIMIT_DISABLED"); ok && v != "" {
		if v != "true" && v != "false" {
			return fmt.Errorf("PHOTODROP_RATE_LIMIT_DISABLED must be true or false")
		}
		s.RateDisabled = v == "true"
	}
	if v, ok := lookup("PHOTODROP_TRUSTED_PROXY_CIDRS"); ok && v != "" {
		parts := strings.Split(v, ",")
		if len(parts) > 32 {
			return fmt.Errorf("PHOTODROP_TRUSTED_PROXY_CIDRS supports at most 32 networks")
		}
		for _, part := range parts {
			p, err := netip.ParsePrefix(strings.TrimSpace(part))
			if err != nil {
				return fmt.Errorf("PHOTODROP_TRUSTED_PROXY_CIDRS must contain valid IPv4/IPv6 CIDRs")
			}
			s.TrustedProxies = append(s.TrustedProxies, p.Masked())
		}
	}
	c.Security = s
	return nil
}
