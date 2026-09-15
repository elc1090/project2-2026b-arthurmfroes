// Package config reads the configuration of one logical Acervo node.
package config

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	AdminProfilesFile                         string
	BootstrapNode                             bool
	EnableDevFaults                           bool
	AdminLogin, AdminPassword                 string
	Port                                      int
	NodeID                                    string
	DatabaseURL                               string
	S3Endpoint                                string
	S3Bucket                                  string
	S3AccessKey                               string
	S3SecretKey                               string
	BackendEndpoint                           string
	ControlToken                              string
	ControlInterval, ControlTimeout, LeaseTTL time.Duration
	FailureThreshold                          int
	SecureCookies                             bool
}

// Load validates configuration without contacting its endpoints. Errors identify
// the setting, never its value, because URLs and credentials may contain secrets.
func Load(getenv func(string) string) (Config, error) {
	c := Config{Port: 8080, ControlInterval: 2 * time.Second, ControlTimeout: 2 * time.Second, LeaseTTL: 15 * time.Second, FailureThreshold: 3}
	if raw := getenv("PORT"); raw != "" {
		port, err := strconv.Atoi(raw)
		if err != nil || port < 1 || port > 65535 || strings.Trim(raw, "0123456789") != "" {
			return Config{}, fmt.Errorf("PORT must be an integer between 1 and 65535")
		}
		c.Port = port
	}
	for _, setting := range []struct {
		name string
		dest *string
	}{
		{"NODE_ID", &c.NodeID},
		{"BACKEND_ENDPOINT", &c.BackendEndpoint},
		{"CONTROL_TOKEN", &c.ControlToken},
		{"DATABASE_URL", &c.DatabaseURL},
		{"S3_ENDPOINT", &c.S3Endpoint},
		{"S3_BUCKET", &c.S3Bucket},
		{"S3_ACCESS_KEY", &c.S3AccessKey},
		{"S3_SECRET_KEY", &c.S3SecretKey},
	} {
		*setting.dest = getenv(setting.name)
		if strings.TrimSpace(*setting.dest) == "" {
			return Config{}, fmt.Errorf("%s is required", setting.name)
		}
	}
	if strings.ContainsAny(c.NodeID, " \t\r\n") {
		return Config{}, fmt.Errorf("NODE_ID must not contain whitespace")
	}
	if err := validateURL(c.DatabaseURL, "DATABASE_URL", true); err != nil {
		return Config{}, err
	}
	if err := validateURL(c.S3Endpoint, "S3_ENDPOINT", false); err != nil {
		return Config{}, err
	}
	if err := validateURL(c.BackendEndpoint, "BACKEND_ENDPOINT", false); err != nil {
		return Config{}, err
	}
	endpoint, _ := url.Parse(c.BackendEndpoint)
	if endpoint.Path != "" && endpoint.Path != "/" {
		return Config{}, fmt.Errorf("BACKEND_ENDPOINT must be an origin without a path")
	}
	for _, setting := range []struct {
		name string
		dest *time.Duration
	}{{"CONTROL_INTERVAL", &c.ControlInterval}, {"CONTROL_TIMEOUT", &c.ControlTimeout}, {"MANAGER_LEASE_TTL", &c.LeaseTTL}} {
		if raw := getenv(setting.name); raw != "" {
			value, err := time.ParseDuration(raw)
			if err != nil || value <= 0 {
				return Config{}, fmt.Errorf("%s must be a positive duration", setting.name)
			}
			*setting.dest = value
		}
	}
	if raw := getenv("FAILURE_THRESHOLD"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 {
			return Config{}, fmt.Errorf("FAILURE_THRESHOLD must be positive")
		}
		c.FailureThreshold = value
	}
	if c.LeaseTTL <= c.ControlInterval+c.ControlTimeout {
		return Config{}, fmt.Errorf("MANAGER_LEASE_TTL must exceed CONTROL_INTERVAL plus CONTROL_TIMEOUT")
	}
	if raw := getenv("SECURE_COOKIES"); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return Config{}, fmt.Errorf("SECURE_COOKIES must be a boolean")
		}
		c.SecureCookies = value
	}
	if raw := getenv("ENABLE_DEV_FAULTS"); raw != "" {
		enabled, err := strconv.ParseBool(raw)
		if err != nil {
			return Config{}, fmt.Errorf("ENABLE_DEV_FAULTS must be a boolean")
		}
		c.EnableDevFaults = enabled
	}
	if raw := getenv("NODE_BOOTSTRAP"); raw != "" {
		bootstrap, err := strconv.ParseBool(raw)
		if err != nil {
			return Config{}, fmt.Errorf("NODE_BOOTSTRAP must be a boolean")
		}
		c.BootstrapNode = bootstrap
	}
	c.AdminLogin = getenv("ADMIN_LOGIN")
	c.AdminProfilesFile = getenv("ADMIN_PROFILES_FILE")
	c.AdminPassword = getenv("ADMIN_PASSWORD")
	if (c.AdminLogin == "") != (c.AdminPassword == "") {
		return Config{}, fmt.Errorf("ADMIN_LOGIN and ADMIN_PASSWORD must be configured together")
	}
	return c, nil
}

func validateURL(raw, name string, database bool) error {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.Fragment != "" || u.Opaque != "" {
		return fmt.Errorf("%s must be an absolute endpoint URL without a fragment", name)
	}
	if database {
		if u.Scheme != "postgres" && u.Scheme != "postgresql" {
			return fmt.Errorf("%s must use postgres or postgresql", name)
		}
	} else if (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.ForceQuery {
		return fmt.Errorf("%s must use http or https without userinfo or query parameters", name)
	}
	if strings.HasSuffix(u.Host, ":") {
		return fmt.Errorf("%s has an empty port", name)
	}
	if rawPort := u.Port(); rawPort != "" {
		port, err := strconv.Atoi(rawPort)
		if err != nil || port < 1 || port > 65535 {
			return fmt.Errorf("%s has an invalid port", name)
		}
	}
	return nil
}
