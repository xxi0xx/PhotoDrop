package config

import (
	"fmt"
	"regexp"
	"strings"
)

const LocalBackendKey = "local-default"

var backendKey = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)

func ValidBackendKey(key string) bool { return len(key) <= 63 && backendKey.MatchString(key) }

func (c *Config) loadStorage(lookup func(string) (string, bool)) error {
	// Parse the existing single-backend syntax without requiring it when the
	// active destination is supplied through the named-backend list instead.
	if err := c.loadStorageSingle(func(k string) (string, bool) {
		if k == "PHOTODROP_STORAGE_PROVIDER" {
			return "local", true
		}
		return lookup(k)
	}); err != nil {
		return err
	}
	if v, ok := lookup("PHOTODROP_STORAGE_PROVIDER"); ok {
		c.StorageProvider = v
	}
	if c.StorageProvider != "local" && c.StorageProvider != "s3" {
		return fmt.Errorf("PHOTODROP_STORAGE_PROVIDER must be local or s3")
	}
	if value, ok := lookup("PHOTODROP_STORAGE_BACKEND_KEY"); ok {
		c.StorageBackendKey = value
	}
	if c.StorageBackendKey != "" && !ValidBackendKey(c.StorageBackendKey) {
		return fmt.Errorf("PHOTODROP_STORAGE_BACKEND_KEY must be 1–63 lowercase letters/digits separated by hyphens, starting with a letter")
	}
	c.S3Backends = map[string]S3{}
	if c.S3.Configured() {
		if c.StorageBackendKey == "" || c.StorageBackendKey == LocalBackendKey {
			return fmt.Errorf("set PHOTODROP_STORAGE_BACKEND_KEY to a stable S3 destination name; local-default is reserved")
		}
		c.S3Backends[c.StorageBackendKey] = c.S3
	}
	list := ""
	if value, ok := lookup("PHOTODROP_S3_BACKENDS"); ok {
		list = value
	}
	if list != "" {
		keys := strings.Split(list, ",")
		if len(keys) > 32 {
			return fmt.Errorf("PHOTODROP_S3_BACKENDS supports at most 32 backend keys")
		}
		for _, key := range keys {
			if !ValidBackendKey(key) || key == LocalBackendKey {
				return fmt.Errorf("PHOTODROP_S3_BACKENDS contains an invalid or reserved backend key")
			}
			if _, exists := c.S3Backends[key]; exists {
				return fmt.Errorf("storage backend %q is configured more than once", key)
			}
			prefix := "PHOTODROP_S3_" + strings.ToUpper(strings.ReplaceAll(key, "-", "_")) + "_"
			var one Config
			if err := one.loadStorageSingle(func(k string) (string, bool) {
				if k == "PHOTODROP_STORAGE_PROVIDER" {
					return "s3", true
				}
				return lookup(prefix + strings.TrimPrefix(k, "PHOTODROP_S3_"))
			}); err != nil {
				return fmt.Errorf("storage backend %q: %w", key, err)
			}
			c.S3Backends[key] = one.S3
		}
	}
	if c.StorageProvider == "s3" {
		if _, ok := c.S3Backends[c.StorageBackendKey]; !ok {
			return fmt.Errorf("PHOTODROP_STORAGE_BACKEND_KEY must select a configured S3 backend")
		}
	} else if c.StorageBackendKey == "" {
		c.StorageBackendKey = LocalBackendKey
	}
	return nil
}
