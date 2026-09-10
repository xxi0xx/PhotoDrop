package config

import (
	"strings"
	"testing"
)

func TestMissingAdministratorPassword(t *testing.T) {
	_, err := parse(func(string) (string, bool) { return "", false })
	if err == nil || !strings.Contains(err.Error(), "PHOTODROP_ADMIN_PASSWORD is required") {
		t.Fatalf("missing credential must fail explicitly: %v", err)
	}
}

func TestCanonicalBaseURL(t *testing.T) {
	for input, expected := range map[string]string{
		"https://Photos.Example.com:443/": "https://photos.example.com",
		"http://LOCALHOST:80/":            "http://localhost",
		"https://[::1]:443/":              "https://[::1]",
		"http://localhost:8080/":          "http://localhost:8080",
	} {
		t.Run(input, func(t *testing.T) {
			cfg, err := parse(func(key string) (string, bool) {
				if key == "PHOTODROP_BASE_URL" {
					return input, true
				}
				return "test-password-for-config", key == "PHOTODROP_ADMIN_PASSWORD"
			})
			if err != nil || cfg.BaseURL != expected {
				t.Fatalf("base URL = %q, error = %v; want %q", cfg.BaseURL, err, expected)
			}
		})
	}
}

func TestDefaults(t *testing.T) {
	cfg, err := parse(func(key string) (string, bool) { return "test-password-for-config", key == "PHOTODROP_ADMIN_PASSWORD" })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddr != ":8080" || cfg.DataDir != "/data" || cfg.BaseURL != "" {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
}

func TestEnvironment(t *testing.T) {
	t.Setenv("PHOTODROP_ADMIN_PASSWORD", "test-password-for-config")
	t.Setenv("PHOTODROP_LISTEN_ADDR", "127.0.0.1:9090")
	t.Setenv("PHOTODROP_DATA_DIR", "./my data")
	t.Setenv("PHOTODROP_BASE_URL", "https://photos.example.com/")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddr != "127.0.0.1:9090" || cfg.DataDir != "./my data" || cfg.BaseURL != "https://photos.example.com" {
		t.Fatalf("environment not applied: %+v", cfg)
	}
}

func TestValidation(t *testing.T) {
	for _, tt := range []struct {
		key, value string
		valid      bool
	}{
		{"PHOTODROP_LISTEN_ADDR", "", false},
		{"PHOTODROP_LISTEN_ADDR", "8080", false},
		{"PHOTODROP_LISTEN_ADDR", ":0", false},
		{"PHOTODROP_LISTEN_ADDR", ":65536", false},
		{"PHOTODROP_LISTEN_ADDR", ":http", false},
		{"PHOTODROP_LISTEN_ADDR", "bad host:8080", false},
		{"PHOTODROP_LISTEN_ADDR", "http://localhost:8080", false},
		{"PHOTODROP_LISTEN_ADDR", "[::1]:8080", true},
		{"PHOTODROP_LISTEN_ADDR", "localhost:8080", true},
		{"PHOTODROP_DATA_DIR", "", false},
		{"PHOTODROP_DATA_DIR", "  ", false},
		{"PHOTODROP_DATA_DIR", "bad\x00path", false},
		{"PHOTODROP_DATA_DIR", "./my data", true},
		{"PHOTODROP_BASE_URL", "", true},
		{"PHOTODROP_BASE_URL", "https://photos.example.com", true},
		{"PHOTODROP_BASE_URL", "http://[::1]:8080/", true},
		{"PHOTODROP_BASE_URL", "photos.example.com", false},
		{"PHOTODROP_BASE_URL", "ftp://photos.example.com", false},
		{"PHOTODROP_BASE_URL", "http://", false},
		{"PHOTODROP_BASE_URL", "http://user:secret@localhost", false},
		{"PHOTODROP_BASE_URL", "http://localhost:99999", false},
		{"PHOTODROP_BASE_URL", "http://localhost:", false},
		{"PHOTODROP_BASE_URL", "http://localhost/path", false},
		{"PHOTODROP_BASE_URL", "http://localhost?x=1", false},
		{"PHOTODROP_BASE_URL", "http://localhost?", false},
		{"PHOTODROP_BASE_URL", "http://localhost#", false},
		{"PHOTODROP_BASE_URL", "http://%zz", false},
		{"PHOTODROP_ADMIN_PASSWORD", "", false},
		{"PHOTODROP_ADMIN_PASSWORD", "too-short", false},
		{"PHOTODROP_ADMIN_PASSWORD", strings.Repeat("x", 73), false},
		{"PHOTODROP_ADMIN_PASSWORD", "password-with-\x00-null", false},
		{"PHOTODROP_ADMIN_PASSWORD", "            ", false},
		{"PHOTODROP_ADMIN_PASSWORD", "test-password-for-config", true},
	} {
		t.Run(tt.key+"/"+tt.value, func(t *testing.T) {
			_, err := parse(func(key string) (string, bool) {
				if key == tt.key {
					return tt.value, true
				}
				return "test-password-for-config", key == "PHOTODROP_ADMIN_PASSWORD"
			})
			if (err == nil) != tt.valid {
				t.Fatalf("valid=%v, got error %v", tt.valid, err)
			}
		})
	}
}
