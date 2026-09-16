package config

import "testing"

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
