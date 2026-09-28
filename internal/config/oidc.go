package config

import (
	"fmt"
	"net"
	"net/url"
	"strings"
	"unicode"
)

type OIDC struct {
	Issuer, ClientID, ClientSecret string
	AllowedSubjects, AllowedGroups []string
	GroupsClaim                    string
	Scopes                         []string
}

func (OIDC) String() string     { return "OIDC configuration (redacted)" }
func (c OIDC) GoString() string { return c.String() }
func (c Config) PasswordEnabled() bool {
	return c.AdminAuth == "" || c.AdminAuth == "password" || c.AdminAuth == "password+oidc"
}
func (c Config) OIDCEnabled() bool { return c.AdminAuth == "oidc" || c.AdminAuth == "password+oidc" }

// OIDC permits path-bearing issuers. Preserve exact spelling for issuer matching.
// Endpoint requests also use this transport rule; HTTP is loopback-only.
func ValidOIDCURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 2048 || u.Opaque != "" || !validHost(u.Hostname()) || u.User != nil || strings.Contains(raw, "#") || strings.HasSuffix(u.Host, ":") || (u.Port() != "" && !validPort(u.Port())) {
		return false
	}
	if u.Scheme == "https" {
		return true
	}
	ip := net.ParseIP(u.Hostname())
	return u.Scheme == "http" && (u.Hostname() == "localhost" || ip != nil && ip.IsLoopback())
}

func (c *Config) loadAuth(lookup func(string) (string, bool)) error {
	c.AdminAuth = "password"
	if value, ok := lookup("PHOTODROP_ADMIN_AUTH"); ok {
		c.AdminAuth = value
	}
	if c.AdminAuth != "password" && c.AdminAuth != "oidc" && c.AdminAuth != "password+oidc" {
		return fmt.Errorf("PHOTODROP_ADMIN_AUTH must be password, oidc, or password+oidc")
	}
	if !c.OIDCEnabled() {
		return nil
	}
	if c.BaseURL == "" || !ValidOIDCURL(c.BaseURL) {
		return fmt.Errorf("OIDC requires PHOTODROP_BASE_URL with HTTPS (HTTP only on loopback)")
	}
	for key, dst := range map[string]*string{
		"PHOTODROP_OIDC_ISSUER":        &c.OIDC.Issuer,
		"PHOTODROP_OIDC_CLIENT_ID":     &c.OIDC.ClientID,
		"PHOTODROP_OIDC_CLIENT_SECRET": &c.OIDC.ClientSecret,
	} {
		*dst, _ = lookup(key)
		if strings.TrimSpace(*dst) == "" || len(*dst) > 4096 || strings.IndexFunc(*dst, unicode.IsControl) >= 0 {
			return fmt.Errorf("%s is required and must not contain control characters", key)
		}
	}
	u, err := url.Parse(c.OIDC.Issuer)
	if err != nil || !ValidOIDCURL(c.OIDC.Issuer) || u.RawQuery != "" || u.ForceQuery {
		return fmt.Errorf("PHOTODROP_OIDC_ISSUER must be an exact HTTPS issuer URL without credentials, query or fragment (HTTP only on loopback)")
	}
	for key, dst := range map[string]*[]string{"PHOTODROP_OIDC_ALLOWED_SUBJECTS": &c.OIDC.AllowedSubjects, "PHOTODROP_OIDC_ALLOWED_GROUPS": &c.OIDC.AllowedGroups} {
		raw, _ := lookup(key)
		if raw == "" {
			continue
		}
		if len(raw) > 8192 || strings.IndexFunc(raw, unicode.IsControl) >= 0 {
			return fmt.Errorf("%s is too long", key)
		}
		for _, part := range strings.Split(raw, ",") {
			value := strings.TrimSpace(part)
			if value == "" || len(value) > 512 || strings.IndexFunc(value, unicode.IsControl) >= 0 {
				return fmt.Errorf("%s must be a comma-separated non-empty allowlist", key)
			}
			*dst = append(*dst, value)
		}
		if len(*dst) > 64 {
			return fmt.Errorf("%s allows at most 64 entries", key)
		}
	}
	if len(c.OIDC.AllowedSubjects)+len(c.OIDC.AllowedGroups) == 0 {
		return fmt.Errorf("OIDC requires PHOTODROP_OIDC_ALLOWED_SUBJECTS and/or PHOTODROP_OIDC_ALLOWED_GROUPS")
	}
	c.OIDC.GroupsClaim = "groups"
	if value, ok := lookup("PHOTODROP_OIDC_GROUPS_CLAIM"); ok {
		c.OIDC.GroupsClaim = value
	}
	if c.OIDC.GroupsClaim == "" || len(c.OIDC.GroupsClaim) > 256 || strings.IndexFunc(c.OIDC.GroupsClaim, unicode.IsSpace) >= 0 || strings.IndexFunc(c.OIDC.GroupsClaim, unicode.IsControl) >= 0 {
		return fmt.Errorf("PHOTODROP_OIDC_GROUPS_CLAIM must be one non-empty JSON claim name")
	}
	raw := "openid profile email"
	if value, ok := lookup("PHOTODROP_OIDC_SCOPES"); ok {
		raw = value
	}
	if raw == "" || len(raw) > 1024 {
		return fmt.Errorf("PHOTODROP_OIDC_SCOPES must be space-separated OAuth scope tokens")
	}
	c.OIDC.Scopes = []string{"openid"}
	seen := map[string]bool{"openid": true}
	for _, scope := range strings.Split(raw, " ") {
		if scope == "" {
			return fmt.Errorf("PHOTODROP_OIDC_SCOPES contains an empty scope")
		}
		for _, ch := range scope {
			if ch < 0x21 || ch > 0x7e || ch == '"' || ch == '\\' {
				return fmt.Errorf("PHOTODROP_OIDC_SCOPES contains an invalid scope")
			}
		}
		if !seen[scope] {
			c.OIDC.Scopes = append(c.OIDC.Scopes, scope)
			seen[scope] = true
		}
	}
	return nil
}
