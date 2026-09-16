package config

import (
	"errors"
	"os"
	"strings"
)

// Export only needs source storage configuration. Authentication, Turnstile,
// and optional integrations are deliberately irrelevant to a trusted local CLI.
func LoadExport() (Config, error) {
	c := Config{DataDir: "/data"}
	if value, ok := os.LookupEnv("PHOTODROP_DATA_DIR"); ok {
		c.DataDir = value
	}
	if strings.TrimSpace(c.DataDir) == "" || strings.ContainsAny(c.DataDir, "\x00\r\n") {
		return Config{}, errors.New("PHOTODROP_DATA_DIR must be a filesystem path")
	}
	if err := c.loadStorage(os.LookupEnv); err != nil {
		return Config{}, err
	}
	return c, nil
}
