// Package config owns the environment configuration for PhotoDrop.
package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	ListenAddr    string
	DataDir       string
	BaseURL       string
	AdminPassword string `json:"-"`
}

func Load() (Config, error) {
	return parse(os.LookupEnv)
}

func parse(lookup func(string) (string, bool)) (Config, error) {
	cfg := Config{ListenAddr: ":8080", DataDir: "/data"}
	for key, dst := range map[string]*string{
		"PHOTODROP_LISTEN_ADDR":    &cfg.ListenAddr,
		"PHOTODROP_DATA_DIR":       &cfg.DataDir,
		"PHOTODROP_BASE_URL":       &cfg.BaseURL,
		"PHOTODROP_ADMIN_PASSWORD": &cfg.AdminPassword,
	} {
		if value, ok := lookup(key); ok {
			*dst = value
		}
	}

	host, port, err := net.SplitHostPort(cfg.ListenAddr)
	if err != nil || !validPort(port) || (host != "" && !validHost(host)) {
		return Config{}, fmt.Errorf("PHOTODROP_LISTEN_ADDR must be host:port (for example :8080), with a port from 1 to 65535")
	}
	if strings.TrimSpace(cfg.DataDir) == "" || strings.ContainsAny(cfg.DataDir, "\x00\r\n") {
		return Config{}, fmt.Errorf("PHOTODROP_DATA_DIR must be a non-empty filesystem path")
	}
	if cfg.BaseURL != "" {
		u, err := url.Parse(cfg.BaseURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") ||
			!validHost(u.Hostname()) || u.User != nil || u.Opaque != "" ||
			(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery ||
			strings.Contains(cfg.BaseURL, "#") || strings.HasSuffix(u.Host, ":") ||
			(u.Port() != "" && !validPort(u.Port())) {
			return Config{}, fmt.Errorf("PHOTODROP_BASE_URL must be an http(s) origin without credentials, a subpath, query, or fragment (for example https://photos.example.com)")
		}
		u.Host = strings.ToLower(u.Host)
		if (u.Scheme == "https" && u.Port() == "443") || (u.Scheme == "http" && u.Port() == "80") {
			u.Host = u.Hostname()
			if strings.Contains(u.Host, ":") {
				u.Host = "[" + u.Host + "]"
			}
		}
		cfg.BaseURL = strings.TrimSuffix(u.String(), "/")
	}
	if len(cfg.AdminPassword) < 12 || len(cfg.AdminPassword) > 72 || strings.TrimSpace(cfg.AdminPassword) == "" || strings.ContainsRune(cfg.AdminPassword, '\x00') {
		return Config{}, fmt.Errorf("PHOTODROP_ADMIN_PASSWORD is required and must contain 12 to 72 bytes; no default administrator password is provided")
	}
	return cfg, nil
}

func validPort(port string) bool {
	if port == "" || strings.Trim(port, "0123456789") != "" {
		return false
	}
	n, err := strconv.Atoi(port)
	return err == nil && n >= 1 && n <= 65535
}

func validHost(host string) bool {
	if net.ParseIP(host) != nil {
		return true
	}
	if len(host) == 0 || len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(strings.TrimSuffix(host, "."), ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}
