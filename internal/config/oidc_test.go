package config

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func oidcEnv() map[string]string {
	return map[string]string{
		"PHOTODROP_ADMIN_AUTH": "oidc", "PHOTODROP_BASE_URL": "https://drop.example.com",
		"PHOTODROP_OIDC_ISSUER":    "https://auth.example.com/application/o/drop/",
		"PHOTODROP_OIDC_CLIENT_ID": "private-client-id", "PHOTODROP_OIDC_CLIENT_SECRET": "private-client-secret",
		"PHOTODROP_OIDC_ALLOWED_GROUPS": "photo-admins",
	}
}
func parseEnv(env map[string]string) (Config, error) {
	return parse(func(key string) (string, bool) { v, ok := env[key]; return v, ok })
}
func TestOIDCConfiguration(t *testing.T) {
	for _, mode := range []string{"password", "oidc", "password+oidc"} {
		env := oidcEnv()
		env["PHOTODROP_ADMIN_AUTH"] = mode
		if mode != "oidc" {
			env["PHOTODROP_ADMIN_PASSWORD"] = "existing-admin-password"
		}
		c, err := parseEnv(env)
		if err != nil {
			t.Fatal(err)
		}
		if c.PasswordEnabled() != (mode != "oidc") || c.OIDCEnabled() != (mode != "password") {
			t.Fatal("wrong mode")
		}
		if mode != "password" && c.OIDC.Issuer != env["PHOTODROP_OIDC_ISSUER"] {
			t.Fatal("issuer rewritten")
		}
		b, _ := json.Marshal(c)
		for _, text := range []string{fmt.Sprint(c), fmt.Sprintf("%#v", c), fmt.Sprint(c.OIDC), string(b)} {
			if strings.Contains(text, "private-client") {
				t.Fatal("configuration leaked", text)
			}
		}
	}
	for key, values := range map[string][]string{
		"PHOTODROP_ADMIN_AUTH":     {"", "OIDC", "proxy"},
		"PHOTODROP_BASE_URL":       {"", "http://drop.example.com"},
		"PHOTODROP_OIDC_ISSUER":    {"", "https://user:pass@auth.test/path", "https://auth.test/path?x=1", "https://auth.test/path?", "https://auth.test/#", "http://auth.test/path", "https://auth.test:0/path", "ftp://auth.test/path"},
		"PHOTODROP_OIDC_CLIENT_ID": {"", "bad\nvalue"}, "PHOTODROP_OIDC_CLIENT_SECRET": {"", "bad\x00value"},
		"PHOTODROP_OIDC_ALLOWED_GROUPS": {"", "a,,b", "a,\nb"},
		"PHOTODROP_OIDC_GROUPS_CLAIM":   {"", "bad claim"},
		"PHOTODROP_OIDC_SCOPES":         {"", "openid  profile", "openid\temail", "openid bad\\scope", "openid bad\"scope"},
	} {
		for _, value := range values {
			t.Run(key+value, func(t *testing.T) {
				env := oidcEnv()
				env[key] = value
				if _, err := parseEnv(env); err == nil {
					t.Fatal("invalid configuration accepted")
				}
			})
		}
	}
	for _, issuer := range []string{"https://auth.test/path/", "http://localhost:5555/application/o/test/", "http://127.0.0.1:5555/", "http://[::1]/"} {
		env := oidcEnv()
		env["PHOTODROP_OIDC_ISSUER"] = issuer
		env["PHOTODROP_OIDC_SCOPES"] = "profile custom"
		c, err := parseEnv(env)
		if err != nil || c.OIDC.Issuer != issuer || strings.Join(c.OIDC.Scopes, " ") != "openid profile custom" {
			t.Fatal(c, err)
		}
	}
}
