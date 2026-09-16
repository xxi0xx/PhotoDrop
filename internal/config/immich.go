package config

import (
	"errors"
	"net/url"
	"strings"
)

type Immich struct {
	URL    string
	APIKey string `json:"-"`
}

func (Immich) String() string     { return "Immich configuration (credentials redacted)" }
func (c Immich) GoString() string { return c.String() }

func NormalizeImmichURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || !validHost(u.Hostname()) || u.User != nil || u.Opaque != "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || strings.Contains(raw, "#") || strings.HasSuffix(u.Host, ":") || (u.Port() != "" && !validPort(u.Port())) {
		return "", errors.New("Immich URL must be an http(s) origin without credentials, path, query, or fragment")
	}
	u.Host = strings.ToLower(u.Host)
	if (u.Scheme == "https" && u.Port() == "443") || (u.Scheme == "http" && u.Port() == "80") {
		u.Host = u.Hostname()
		if strings.Contains(u.Host, ":") {
			u.Host = "[" + u.Host + "]"
		}
	}
	u.Path = ""
	return u.String(), nil
}

func (c *Config) loadImmich(lookup func(string) (string, bool)) error {
	original := lookup
	lookup = func(key string) (string, bool) {
		value, ok := original(key)
		if !ok {
			return "", false
		}
		return value, true
	}
	c.ImmichTarget, _ = lookup("PHOTODROP_IMMICH_TARGET")
	if c.ImmichTarget != "" && !ValidBackendKey(c.ImmichTarget) {
		return errors.New("PHOTODROP_IMMICH_TARGET must be a valid named target key")
	}
	list, _ := lookup("PHOTODROP_IMMICH_TARGETS")
	if list == "" {
		list = c.ImmichTarget
	}
	c.ImmichTargets = map[string]Immich{}
	if list == "" {
		return nil
	}
	keys := strings.Split(list, ",")
	if len(keys) > 32 {
		return errors.New("PHOTODROP_IMMICH_TARGETS supports at most 32 keys")
	}
	for _, key := range keys {
		if !ValidBackendKey(key) {
			return errors.New("invalid Immich target key")
		}
		if _, ok := c.ImmichTargets[key]; ok {
			return errors.New("duplicate Immich target key")
		}
		prefix := "PHOTODROP_IMMICH_" + strings.ToUpper(strings.ReplaceAll(key, "-", "_")) + "_"
		raw, _ := lookup(prefix + "URL")
		secret, _ := lookup(prefix + "API_KEY")
		if len(secret) > 4096 || strings.ContainsAny(secret, "\x00\r\n") {
			return errors.New("Immich API key contains invalid characters or is too long")
		}
		// Missing credentials may be intentional for a historical target. An
		// absent URL also defers to its persisted identity without enabling it.
		if raw == "" && secret == "" {
			c.ImmichTargets[key] = Immich{}
			continue
		}
		base, err := NormalizeImmichURL(raw)
		if err != nil {
			return err
		}
		c.ImmichTargets[key] = Immich{URL: base, APIKey: secret}
	}
	return nil
}
