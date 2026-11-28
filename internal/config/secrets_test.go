package config

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestSecretsKeyFileConfiguration(t *testing.T) {
	parseEnv := func(env map[string]string) (Config, error) {
		env["PHOTODROP_ADMIN_PASSWORD"] = "test-password-for-config"
		return parse(func(key string) (string, bool) { v, ok := env[key]; return v, ok })
	}
	c, err := parseEnv(map[string]string{})
	if err != nil || c.Secrets.KeyFile != "" || c.Secrets.PreviousKeyFiles != nil {
		t.Fatal("no key must be the default", c.Secrets, err)
	}
	env := map[string]string{
		"PHOTODROP_SECRETS_KEY_FILE":           " /run/secrets/photodrop_secrets_key ",
		"PHOTODROP_SECRETS_PREVIOUS_KEY_FILES": "/run/secrets/old-1, /run/secrets/old-2",
	}
	c, err = parseEnv(env)
	if err != nil || c.Secrets.KeyFile != "/run/secrets/photodrop_secrets_key" || strings.Join(c.Secrets.PreviousKeyFiles, "|") != "/run/secrets/old-1|/run/secrets/old-2" {
		t.Fatal(c.Secrets, err)
	}
	b, _ := json.Marshal(c)
	if strings.Contains(fmt.Sprint(c)+fmt.Sprintf("%#v", c)+string(b), "photodrop_secrets_key") {
		t.Fatal("key file path appeared in formatted configuration")
	}
	for name, bad := range map[string]map[string]string{
		"previous without active": {"PHOTODROP_SECRETS_PREVIOUS_KEY_FILES": "/run/secrets/old"},
		"empty previous entry":    {"PHOTODROP_SECRETS_KEY_FILE": "/k", "PHOTODROP_SECRETS_PREVIOUS_KEY_FILES": "/a,,/b"},
		"control character":       {"PHOTODROP_SECRETS_KEY_FILE": "/k\n"},
		"blank path":              {"PHOTODROP_SECRETS_KEY_FILE": "   "},
		"too many previous":       {"PHOTODROP_SECRETS_KEY_FILE": "/k", "PHOTODROP_SECRETS_PREVIOUS_KEY_FILES": "/1,/2,/3,/4,/5,/6,/7,/8,/9"},
	} {
		if _, err := parseEnv(bad); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}
