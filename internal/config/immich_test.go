package config

import (
	"strings"
	"testing"
)

func TestImmichPublicURL(t *testing.T) {
	for _, raw := range []string{"", "https://PHOTOS.EXAMPLE:443/", "http://localhost:2283", "javascript:alert(1)", "https://user:secret@photos.test", "https://photos.test/api", "https://photos.test?secret=hidden", "https://photos.test#fragment"} {
		t.Run(raw, func(t *testing.T) {
			values := map[string]string{"PHOTODROP_IMMICH_TARGET": "home", "PHOTODROP_IMMICH_HOME_PUBLIC_URL": raw}
			var cfg Config
			err := cfg.loadImmich(func(k string) (string, bool) { v, ok := values[k]; return v, ok })
			expected, validation := NormalizeImmichURL(raw)
			if raw != "" && validation != nil {
				if err == nil || strings.Contains(err.Error(), raw) {
					t.Fatal("invalid origin accepted/leaked", err)
				}
				return
			}
			if err != nil || cfg.ImmichTargets["home"].PublicURL != expected {
				t.Fatal(cfg, err)
			}
		})
	}
}

func TestImmichConfig(t *testing.T) {
	values := map[string]string{"PHOTODROP_IMMICH_TARGET": "home", "PHOTODROP_IMMICH_HOME_URL": "https://PHOTOS.EXAMPLE:443/", "PHOTODROP_IMMICH_HOME_API_KEY": "runtime-secret"}
	lookup := func(k string) (string, bool) { v, ok := values[k]; return v, ok }
	var cfg Config
	if err := cfg.loadImmich(lookup); err != nil {
		t.Fatal(err)
	}
	if cfg.ImmichTargets["home"].URL != "https://photos.example" {
		t.Fatal(cfg.ImmichTargets)
	}
	delete(values, "PHOTODROP_IMMICH_HOME_API_KEY")
	if err := cfg.loadImmich(lookup); err != nil {
		t.Fatal("missing credentials should defer to operation", err)
	}
	for _, bad := range []string{"file:///etc/passwd", "https://user:password@photos.test", "https://photos.test/api", "https://photos.test?key=secret", "https://photos.test#secret", "http://photos.test:0", "https://photos.test/../", "//photos.test"} {
		if _, err := NormalizeImmichURL(bad); err == nil {
			t.Fatal("unsafe URL", bad)
		}
	}
	values["PHOTODROP_IMMICH_HOME_API_KEY"] = "secret\r\nheader"
	if err := cfg.loadImmich(lookup); err == nil {
		t.Fatal("header injection accepted")
	}
}
