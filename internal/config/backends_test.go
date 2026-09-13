package config

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestNamedBackendConfiguration(t *testing.T) {
	values := map[string]string{"PHOTODROP_STORAGE_PROVIDER": "s3", "PHOTODROP_STORAGE_BACKEND_KEY": "s3-b", "PHOTODROP_S3_BACKENDS": "s3-a,s3-b"}
	for _, suffix := range []string{"A", "B"} {
		p := "PHOTODROP_S3_S3_" + suffix + "_"
		values[p+"BUCKET"] = "bucket-" + strings.ToLower(suffix)
		values[p+"ENDPOINT"] = "https://STORE.example/"
		values[p+"PREFIX"] = "/photos/"
		values[p+"ACCESS_KEY_ID"] = "backend-secret-id"
		values[p+"SECRET_ACCESS_KEY"] = "backend-secret-value"
		values[p+"SESSION_TOKEN"] = "backend-secret-token"
	}
	cfg, err := storageConfig(values)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.S3Backends) != 2 || cfg.StorageBackendKey != "s3-b" || cfg.S3Backends["s3-a"].Endpoint != "https://store.example" || cfg.S3Backends["s3-a"].Prefix != "photos/" {
		t.Fatal("incorrect normalized named backends")
	}
	encoded, _ := json.Marshal(cfg)
	rendered := fmt.Sprintf("%v %+v %#v %v %#v %s", cfg, cfg, cfg, cfg.S3Backends, cfg.S3Backends, encoded)
	for _, secret := range []string{"backend-secret-id", "backend-secret-value", "backend-secret-token"} {
		if strings.Contains(rendered, secret) {
			t.Fatal("credential leaked")
		}
	}
	for _, bad := range []string{"../x", "a_b", "a--b", "A", "x, x", "s3-a,s3-a", "local-default", "a\nSECRET", "a;evil", strings.Repeat("a", 64)} {
		copy := map[string]string{}
		for k, v := range values {
			copy[k] = v
		}
		copy["PHOTODROP_S3_BACKENDS"] = bad
		if _, err := storageConfig(copy); err == nil {
			t.Fatalf("accepted backend list %q", bad)
		}
	}
	delete(values, "PHOTODROP_S3_S3_A_SECRET_ACCESS_KEY")
	if _, err := storageConfig(values); err == nil {
		t.Fatal("accepted incomplete named credentials")
	}
	values["PHOTODROP_S3_BACKENDS"] = "s3-b" // Omitted historical credential sets are valid.
	values["PHOTODROP_STORAGE_PROVIDER"] = "local"
	if _, err := storageConfig(values); err != nil {
		t.Fatal(err)
	}
}
