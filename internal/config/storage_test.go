package config

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func storageConfig(values map[string]string) (Config, error) {
	return parse(func(k string) (string, bool) {
		if k == "PHOTODROP_ADMIN_PASSWORD" {
			return "admin-secret-test-value", true
		}
		v, ok := values[k]
		return v, ok
	})
}
func TestStorageConfig(t *testing.T) {
	c, err := storageConfig(nil)
	if err != nil || c.StorageProvider != "local" || c.S3.Configured() {
		t.Fatal("local default requires S3", err)
	}
	good := map[string]string{"PHOTODROP_STORAGE_PROVIDER": "s3", "PHOTODROP_S3_BUCKET": "photodrop", "PHOTODROP_S3_ACCESS_KEY_ID": "private-key-id", "PHOTODROP_S3_SECRET_ACCESS_KEY": "private-secret-value"}
	c, err = storageConfig(good)
	if err != nil || c.S3.Region != "auto" || c.S3.PresignTTL != 10*time.Minute {
		t.Fatal(err)
	}
	for _, change := range []struct{ k, v string }{
		{"STORAGE_PROVIDER", "cloud"}, {"S3_BUCKET", ""}, {"S3_BUCKET", "../bucket"}, {"S3_BUCKET", "127.0.0.1"}, {"S3_REGION", ""}, {"S3_REGION", "bad region"},
		{"S3_ACCESS_KEY_ID", ""}, {"S3_SECRET_ACCESS_KEY", ""}, {"S3_SESSION_TOKEN", "bad\nsecret"}, {"S3_ENDPOINT", "https://user:secret@store.test"},
		{"S3_ENDPOINT", "ftp://store.test"}, {"S3_ENDPOINT", "https://store.test/path"}, {"S3_ENDPOINT", "https://store.test/?secret=x"}, {"S3_ENDPOINT", "http://store.test:99999"},
		{"S3_PRESIGN_TTL", "garbage"}, {"S3_PRESIGN_TTL", "59s"}, {"S3_PRESIGN_TTL", "16m"}, {"S3_PATH_STYLE", "yes"},
		{"S3_PREFIX", "../else"}, {"S3_PREFIX", "foo//bar"}, {"S3_PREFIX", `foo\bar`}, {"S3_PREFIX", "foo/%2e%2e"}, {"S3_PREFIX", "photo.s"},
	} {
		t.Run(change.k+change.v, func(t *testing.T) {
			m := map[string]string{}
			for k, v := range good {
				m[k] = v
			}
			m["PHOTODROP_"+change.k] = change.v
			if _, err := storageConfig(m); err == nil {
				t.Fatal("accepted invalid config")
			}
		})
	}
	good["PHOTODROP_STORAGE_PROVIDER"] = "local"
	good["PHOTODROP_S3_ENDPOINT"] = "http://localhost:9000/"
	good["PHOTODROP_S3_PATH_STYLE"] = "true"
	good["PHOTODROP_S3_PREFIX"] = "/my_photos/test-/"
	good["PHOTODROP_S3_SESSION_TOKEN"] = "private-session-token"
	c, err = storageConfig(good)
	if err != nil || !c.S3.Configured() || c.S3.Prefix != "my_photos/test-/" || !c.S3.PathStyle {
		t.Fatal("historical backend or normalization", err)
	}
	encoded, _ := json.Marshal(c)
	s3encoded, _ := json.Marshal(c.S3)
	outputs := fmt.Sprintf("%v %+v %#v %v %+v %#v %s %s", c, c, c, c.S3, c.S3, c.S3, encoded, s3encoded)
	for _, secret := range []string{"private-key-id", "private-secret-value", "private-session-token", "admin-secret-test-value"} {
		if strings.Contains(outputs, secret) {
			t.Fatal("formatted config leaked a secret")
		}
	}
}
