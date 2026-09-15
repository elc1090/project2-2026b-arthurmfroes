package config

import (
	"strings"
	"testing"
)

func validEnv() map[string]string {
	return map[string]string{
		"NODE_ID":          "node-2",
		"BACKEND_ENDPOINT": "https://backend.example.test",
		"CONTROL_TOKEN":    "test-control-secret",
		"DATABASE_URL":     "postgresql://root:private-password@db.example.test:26257/acervo?sslmode=require",
		"S3_ENDPOINT":      "https://storage.example.test:9000",
		"S3_BUCKET":        "acervo",
		"S3_ACCESS_KEY":    "private-access-key",
		"S3_SECRET_KEY":    "private-secret-key",
	}
}

func TestLoadValid(t *testing.T) {
	for _, tc := range []struct {
		name, port, db, storage string
		wantPort                int
	}{
		{"defaults", "", "postgresql://db.example.test/acervo", "https://storage.example.test", 8080},
		{"local endpoints", "9008", "postgres://root@cockroach-2:26257/drive_clone?sslmode=disable", "http://minio-2:9000", 9008},
		{"IPv6", "65535", "postgresql://[::1]:26257/acervo", "http://[::1]:9000", 65535},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := validEnv()
			env["PORT"], env["DATABASE_URL"], env["S3_ENDPOINT"] = tc.port, tc.db, tc.storage
			cfg, err := Load(func(key string) string { return env[key] })
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Port != tc.wantPort || cfg.NodeID != env["NODE_ID"] || cfg.DatabaseURL != tc.db || cfg.S3Endpoint != tc.storage || cfg.S3Bucket != env["S3_BUCKET"] || cfg.S3AccessKey != env["S3_ACCESS_KEY"] || cfg.S3SecretKey != env["S3_SECRET_KEY"] {
				t.Fatal("configuration did not preserve its settings")
			}
		})
	}
}

func TestLoadRequired(t *testing.T) {
	for name := range validEnv() {
		for _, empty := range []string{"", " \t\n"} {
			t.Run(name+"/"+empty, func(t *testing.T) {
				env := validEnv()
				env[name] = empty
				_, err := Load(func(key string) string { return env[key] })
				if err == nil || !strings.Contains(err.Error(), name) {
					t.Fatalf("expected error identifying %s", name)
				}
			})
		}
	}
}

func TestLoadRejectsInvalidConfiguration(t *testing.T) {
	for name, values := range map[string][]string{
		"PORT":         {"0", "65536", "-1", "8080x", " 8080", "+8080", "99999999999999999999"},
		"NODE_ID":      {"node 2", "node\n2", "node\t2"},
		"DATABASE_URL": {"db:26257/acervo", "https://db/acervo", "postgresql:///acervo", "postgresql://db:0/acervo", "postgresql://db:65536/acervo", "postgresql://db:/acervo", "postgresql://db:abc/acervo", "postgresql://db/acervo#fragment", "postgresql://root:private-password@db/%zz"},
		"S3_ENDPOINT":  {"minio:9000", "ftp://minio", "http:///", "https://minio:0", "http://minio:65536", "http://minio:", "http://minio:invalid", "http://minio/#fragment", "http://private-access-key:private-secret-key@minio", "http://minio?key=private-secret-key", "http://minio?", "http://minio/%zz"},
	} {
		for _, value := range values {
			t.Run(name+"/"+value, func(t *testing.T) {
				env := validEnv()
				env[name] = value
				_, err := Load(func(key string) string { return env[key] })
				if err == nil || !strings.Contains(err.Error(), name) {
					t.Fatalf("expected error identifying %s", name)
				}
				for _, secret := range []string{"private-password", "private-access-key", "private-secret-key"} {
					if strings.Contains(err.Error(), secret) {
						t.Fatal("configuration error disclosed a credential")
					}
				}
			})
		}
	}
}

func TestControlConfiguration(t *testing.T) {
	for name, value := range map[string]string{"NODE_BOOTSTRAP": "maybe", "CONTROL_INTERVAL": "0s", "CONTROL_TIMEOUT": "invalid", "MANAGER_LEASE_TTL": "1s", "FAILURE_THRESHOLD": "0", "SECURE_COOKIES": "maybe", "BACKEND_ENDPOINT": "http://backend/path"} {
		env := validEnv()
		env[name] = value
		if _, err := Load(func(key string) string { return env[key] }); err == nil {
			t.Errorf("accepted %s=%s", name, value)
		}
	}
}

func TestBootstrapRequiresExplicitConfiguration(t *testing.T) {
	for _, value := range []string{"", "false", "true"} {
		env := validEnv()
		env["NODE_BOOTSTRAP"] = value
		cfg, err := Load(func(key string) string { return env[key] })
		if err != nil || cfg.BootstrapNode != (value == "true") {
			t.Fatal("bootstrap setting", value, cfg.BootstrapNode, err)
		}
	}
}
