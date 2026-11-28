package config

import (
	"errors"
	"os"
	"strings"
)

// Secrets names the master key files for stored destination credentials. Only
// paths live in configuration; internal/secrets reads and validates the files.
type Secrets struct {
	KeyFile          string
	PreviousKeyFiles []string
}

const pathControl = "\x00\r\n"

func validKeyPath(path string) bool {
	return path != "" && len(path) <= 4096 && !strings.ContainsAny(path, pathControl)
}

func (c *Config) loadSecrets(lookup func(string) (string, bool)) error {
	c.Secrets = Secrets{}
	if value, ok := lookup("PHOTODROP_SECRETS_KEY_FILE"); ok && value != "" {
		if strings.ContainsAny(value, pathControl) || !validKeyPath(strings.TrimSpace(value)) {
			return errors.New("PHOTODROP_SECRETS_KEY_FILE must be a file path")
		}
		c.Secrets.KeyFile = strings.TrimSpace(value)
	}
	raw, ok := lookup("PHOTODROP_SECRETS_PREVIOUS_KEY_FILES")
	if !ok || raw == "" {
		return nil
	}
	if c.Secrets.KeyFile == "" {
		return errors.New("PHOTODROP_SECRETS_PREVIOUS_KEY_FILES needs PHOTODROP_SECRETS_KEY_FILE")
	}
	if strings.ContainsAny(raw, pathControl) {
		return errors.New("PHOTODROP_SECRETS_PREVIOUS_KEY_FILES must be a comma-separated list of file paths")
	}
	for _, part := range strings.Split(raw, ",") {
		path := strings.TrimSpace(part)
		if !validKeyPath(path) {
			return errors.New("PHOTODROP_SECRETS_PREVIOUS_KEY_FILES must be a comma-separated list of file paths")
		}
		c.Secrets.PreviousKeyFiles = append(c.Secrets.PreviousKeyFiles, path)
	}
	if len(c.Secrets.PreviousKeyFiles) > 8 {
		return errors.New("PHOTODROP_SECRETS_PREVIOUS_KEY_FILES allows at most 8 files")
	}
	return nil
}

// LoadSecrets is for the rotate-secrets command: the data directory and the
// key files, nothing else.
func LoadSecrets() (Config, error) {
	c := Config{DataDir: "/data"}
	if value, ok := os.LookupEnv("PHOTODROP_DATA_DIR"); ok {
		c.DataDir = value
	}
	if strings.TrimSpace(c.DataDir) == "" || strings.ContainsAny(c.DataDir, pathControl) {
		return Config{}, errors.New("PHOTODROP_DATA_DIR must be a filesystem path")
	}
	if err := c.loadSecrets(os.LookupEnv); err != nil {
		return Config{}, err
	}
	return c, nil
}
