package config

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestSecurityConfig(t *testing.T) {
	parseValues := func(values map[string]string) (Config, error) {
		return parse(func(k string) (string, bool) {
			if k == "PHOTODROP_ADMIN_PASSWORD" {
				return "config-test-password", true
			}
			v, ok := values[k]
			return v, ok
		})
	}
	base := map[string]string{"PHOTODROP_BASE_URL": "https://photos.test", "PHOTODROP_TURNSTILE_SITE_KEY": "public-site", "PHOTODROP_TURNSTILE_SECRET_KEY": "private-turnstile-secret", "PHOTODROP_UPLOAD_SESSION_TTL": "1h", "PHOTODROP_UPLOAD_SESSION_MAX_ASSETS": "50", "PHOTODROP_UPLOAD_SESSION_MAX_BYTES": "100000", "PHOTODROP_TRUSTED_PROXY_CIDRS": "10.0.0.0/8,fd00::/8", "PHOTODROP_RATE_LIMIT_MULTIPLIER": "2"}
	cfg, err := parseValues(base)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Security.SessionTTL != time.Hour || cfg.Security.SessionMaxAssets != 50 || cfg.Security.SessionMaxBytes != 100000 || len(cfg.Security.TrustedProxies) != 2 {
		t.Fatal("settings ignored")
	}
	data, _ := json.Marshal(cfg)
	nested, _ := json.Marshal(cfg.Security)
	if strings.Contains(string(data)+string(nested)+fmt.Sprintf("%+v %#v %+v %#v", cfg, cfg, cfg.Security, cfg.Security), "private-turnstile-secret") {
		t.Fatal("secret escaped formatting")
	}
	for key, value := range map[string]string{"PHOTODROP_TURNSTILE_SITE_KEY": "", "PHOTODROP_TURNSTILE_SECRET_KEY": "", "PHOTODROP_BASE_URL": "", "PHOTODROP_UPLOAD_SESSION_TTL": "25h", "PHOTODROP_UPLOAD_SESSION_MAX_ASSETS": "0", "PHOTODROP_UPLOAD_SESSION_MAX_BYTES": "-1", "PHOTODROP_TRUSTED_PROXY_CIDRS": "all", "PHOTODROP_RATE_LIMIT_MULTIPLIER": "0", "PHOTODROP_RATE_LIMIT_DISABLED": "yes"} {
		copy := map[string]string{}
		for k, v := range base {
			copy[k] = v
		}
		copy[key] = value
		if _, err := parseValues(copy); err == nil {
			t.Fatal("accepted invalid", key)
		}
	}
	cfg, err = parseValues(nil)
	if err != nil || cfg.Security.RateDisabled || cfg.Security.SessionTTL != 2*time.Hour || cfg.Security.SessionMaxAssets != 100 || cfg.Security.TurnstileSiteKey != "" {
		t.Fatal("unsafe defaults", err)
	}
}
