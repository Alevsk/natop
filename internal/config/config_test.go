package config

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func isolatedConfig(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("NATS_URL", "")
	dir, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "natop", "config.yaml")
}

func writeConfig(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadPrecedence(t *testing.T) {
	defaultPath := isolatedConfig(t)
	got, err := Load("", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Refresh != 2*time.Second || len(got.Connections) != 1 || got.Connections[0].URL != "nats://localhost:4222" {
		t.Fatalf("unexpected defaults: %+v", got)
	}
	t.Setenv("NATS_URL", "nats://environment:4222")
	got, err = Load("", "", "")
	if err != nil || got.Connections[0].URL != "nats://environment:4222" {
		t.Fatalf("environment fallback: %+v, %v", got, err)
	}
	writeConfig(t, defaultPath, "refresh: 3s\nconnections:\n  - name: file\n    url: nats://file:4222")
	got, err = Load("", "", "")
	if err != nil || got.Refresh != 3*time.Second || got.Connections[0].Name != "file" {
		t.Fatalf("default config: %+v, %v", got, err)
	}
	explicitPath := filepath.Join(t.TempDir(), "explicit.yaml")
	writeConfig(t, explicitPath, "refresh: 4s\nconnections:\n  - name: explicit\n    url: tls://explicit:4222")
	got, err = Load(explicitPath, "", "500ms")
	if err != nil || got.Refresh != 500*time.Millisecond || got.Connections[0].Name != "explicit" {
		t.Fatalf("explicit config and refresh: %+v, %v", got, err)
	}
	got, err = Load(filepath.Join(t.TempDir(), "missing.yaml"), "nats://flag:4222", "1s")
	if err != nil || got.Refresh != time.Second || got.Connections[0].URL != "nats://flag:4222" {
		t.Fatalf("server flag must override the entire file: %+v, %v", got, err)
	}
	if _, err := Load(filepath.Join(t.TempDir(), "missing.yaml"), "", ""); err == nil {
		t.Fatal("missing explicit config accepted")
	}
}

func TestLoadRejectsInvalidConfig(t *testing.T) {
	isolatedConfig(t)
	for _, tc := range []struct{ name, input string }{
		{"empty connections", "connections: []"},
		{"missing connections", ""},
		{"null", "null"},
		{"unknown top level field", "extra: true\nconnections:\n  - name: a\n    url: nats://a"},
		{"unknown connection field", "connections:\n  - name: a\n    url: nats://a\n    extra: true"},
		{"trailing YAML", "connections:\n  - name: a\n    url: nats://a\n---"},
		{"duplicate name", "connections:\n  - name: a\n    url: nats://a\n  - name: a\n    url: nats://b"},
		{"empty name", "connections:\n  - name: \" \"\n    url: nats://a"},
		{"invalid refresh", "refresh: never\nconnections:\n  - name: a\n    url: nats://a"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			writeConfig(t, path, tc.input)
			if _, err := Load(path, "", ""); err == nil {
				t.Fatal("invalid config accepted")
			}
		})
	}
}

func TestLoadRefreshBounds(t *testing.T) {
	isolatedConfig(t)
	for _, input := range []string{"249ms", "0", "-1s", "1h1ns", "not-a-duration"} {
		if _, err := Load("", "", input); err == nil {
			t.Errorf("invalid refresh %q accepted", input)
		}
	}
	for _, input := range []string{"250ms", "1h"} {
		if _, err := Load("", "", input); err != nil {
			t.Errorf("valid refresh %q rejected: %v", input, err)
		}
	}
}

func TestLoadDirectory(t *testing.T) {
	t.Run("merges files in filename order", func(t *testing.T) {
		isolatedConfig(t)
		dir := t.TempDir()
		writeConfig(t, filepath.Join(dir, "team-a.yaml"), "refresh: 3s\nconnections:\n  - name: a1\n    url: nats://a1:4222\n  - name: a2\n    url: nats://a2:4222")
		writeConfig(t, filepath.Join(dir, "team-b.yml"), "connections:\n  - name: b1\n    url: nats://b1:4222\n    credentials: user.creds")
		got, err := Load(dir, "", "")
		if err != nil {
			t.Fatal(err)
		}
		if got.Refresh != 3*time.Second {
			t.Fatalf("expected the sole refresh setting (3s) to apply, got %v", got.Refresh)
		}
		var names []string
		for _, c := range got.Connections {
			names = append(names, c.Name)
		}
		if strings.Join(names, ",") != "a1,a2,b1" {
			t.Fatalf("expected files merged in filename order with each file's own order preserved: %v", names)
		}
		if got.Connections[2].Credentials != filepath.Join(dir, "user.creds") {
			t.Fatalf("credentials should resolve relative to team-b.yml's own directory: %+v", got.Connections[2])
		}
	})

	t.Run("duplicate name across files names both files", func(t *testing.T) {
		isolatedConfig(t)
		dir := t.TempDir()
		writeConfig(t, filepath.Join(dir, "team-a.yaml"), "connections:\n  - name: orders\n    url: nats://a:4222")
		writeConfig(t, filepath.Join(dir, "team-b.yaml"), "connections:\n  - name: other\n    url: nats://b:4222\n  - name: orders\n    url: nats://c:4222")
		_, err := Load(dir, "", "")
		want := `team-b.yaml: connection 2: duplicate name "orders" (already defined in team-a.yaml)`
		if err == nil || err.Error() != want {
			t.Fatalf("error = %v, want %q", err, want)
		}
	})

	t.Run("conflicting refresh values error without an override", func(t *testing.T) {
		isolatedConfig(t)
		dir := t.TempDir()
		writeConfig(t, filepath.Join(dir, "team-a.yaml"), "refresh: 3s\nconnections:\n  - name: a\n    url: nats://a:4222")
		writeConfig(t, filepath.Join(dir, "team-b.yaml"), "refresh: 5s\nconnections:\n  - name: b\n    url: nats://b:4222")
		_, err := Load(dir, "", "")
		if err == nil {
			t.Fatal("conflicting refresh values accepted")
		}
		msg := err.Error()
		if !strings.Contains(msg, "team-a.yaml") || !strings.Contains(msg, "team-b.yaml") || !strings.Contains(msg, "--refresh") {
			t.Fatalf("error should name both conflicting files and suggest --refresh: %v", err)
		}
	})

	t.Run("refresh flag resolves a conflict", func(t *testing.T) {
		isolatedConfig(t)
		dir := t.TempDir()
		writeConfig(t, filepath.Join(dir, "team-a.yaml"), "refresh: 3s\nconnections:\n  - name: a\n    url: nats://a:4222")
		writeConfig(t, filepath.Join(dir, "team-b.yaml"), "refresh: 5s\nconnections:\n  - name: b\n    url: nats://b:4222")
		got, err := Load(dir, "", "1s")
		if err != nil || got.Refresh != time.Second {
			t.Fatalf("--refresh should override conflicting file settings: %+v, %v", got, err)
		}
	})

	t.Run("agreeing refresh values across files are accepted", func(t *testing.T) {
		isolatedConfig(t)
		dir := t.TempDir()
		writeConfig(t, filepath.Join(dir, "team-a.yaml"), "refresh: 4s\nconnections:\n  - name: a\n    url: nats://a:4222")
		writeConfig(t, filepath.Join(dir, "team-b.yaml"), "refresh: 4s\nconnections:\n  - name: b\n    url: nats://b:4222")
		got, err := Load(dir, "", "")
		if err != nil || got.Refresh != 4*time.Second {
			t.Fatalf("agreeing refresh values should be used without error: %+v, %v", got, err)
		}
	})

	t.Run("empty directory is an error", func(t *testing.T) {
		isolatedConfig(t)
		if _, err := Load(t.TempDir(), "", ""); err == nil {
			t.Fatal("empty config directory accepted")
		}
	})
}

func TestConnectionValidate(t *testing.T) {
	for _, rawURL := range []string{"nats://localhost", "tls://localhost:4222", "ws://localhost:8080/nats", "wss://localhost:443", "nats://[::1]:4222", "nats://user:pass@localhost", "nats://token@localhost"} {
		if err := (Connection{Name: "test", URL: rawURL}).Validate(); err != nil {
			t.Errorf("valid URL %q rejected: %v", rawURL, err)
		}
	}
	for _, rawURL := range []string{"", "localhost:4222", "http://localhost:4222", "nats:///", "nats://localhost:0", "nats://localhost:65536", "nats://localhost:abc", "nats://localhost:", "nats://localhost/path", "nats://localhost?foo=bar", "nats://localhost#fragment", "nats://a:4222,nats://b:4222", "nats://a,b", "nats://localhost:4222 "} {
		if err := (Connection{Name: "test", URL: rawURL}).Validate(); err == nil {
			t.Errorf("invalid URL %q accepted", rawURL)
		}
	}
	for _, tc := range []struct {
		name string
		conn Connection
	}{
		{"credentials and token", Connection{Credentials: "a.creds", Token: "secret"}},
		{"credentials and user", Connection{Credentials: "a.creds", User: "user"}},
		{"token and user", Connection{Token: "secret", User: "user"}},
		{"password alone", Connection{Password: "secret"}},
		{"certificate alone", Connection{TLSCert: "client.pem"}},
		{"key alone", Connection{TLSKey: "client.key"}},
		{"URL and field authentication", Connection{URL: "nats://secret@localhost", Token: "other"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.conn.Name = "test"
			if tc.conn.URL == "" {
				tc.conn.URL = "nats://localhost"
			}
			if err := tc.conn.Validate(); err == nil {
				t.Fatal("invalid authentication or TLS config accepted")
			}
		})
	}
}

func TestLoadExpandsEnvironmentAndResolvesPaths(t *testing.T) {
	isolatedConfig(t)
	t.Setenv("CONFIG_TEST_URL", "tls://server:4222")
	t.Setenv("CONFIG_TEST_CREDS", "secrets/user.creds")
	t.Setenv("CONFIG_TEST_CERT", "client.pem")
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeConfig(t, path, "connections:\n  - name: a\n    url: ${CONFIG_TEST_URL}\n    credentials: ${CONFIG_TEST_CREDS}\n    tls_ca: ~/ca.pem\n    tls_cert: ${CONFIG_TEST_CERT}\n    tls_key: client.key")
	got, err := Load(path, "", "")
	if err != nil {
		t.Fatal(err)
	}
	c := got.Connections[0]
	if c.URL != "tls://server:4222" || c.Credentials != filepath.Join(filepath.Dir(path), "secrets/user.creds") || c.TLSCert != filepath.Join(filepath.Dir(path), "client.pem") || c.TLSKey != filepath.Join(filepath.Dir(path), "client.key") || c.TLSCA != filepath.Join(os.Getenv("HOME"), "ca.pem") {
		t.Fatalf("incorrect expansion: %+v", c)
	}
	t.Setenv("CONFIG_TEST_TOKEN", "token-value")
	writeConfig(t, path, "connections:\n  - name: a\n    url: nats://server\n    token: ${CONFIG_TEST_TOKEN}")
	got, err = Load(path, "", "")
	if err != nil || got.Connections[0].Token != "token-value" {
		t.Fatalf("token expansion failed: %+v, %v", got, err)
	}
	writeConfig(t, path, "connections:\n  - name: a\n    url: nats://server\n    token: prefix-${NATOP_TEST_MISSING_VARIABLE}-secret")
	if _, err := Load(path, "", ""); err == nil || strings.Contains(err.Error(), "prefix-") {
		t.Fatalf("missing environment variable should return a safe error: %v", err)
	}
}

func TestSafeURLAndRedact(t *testing.T) {
	c := Connection{URL: "nats://private%2Duser:p%40ss%2Fword@localhost:4222", Token: "token-value", Password: "secret-value", Credentials: "/private/user.creds", TLSKey: "/private/client.key"}
	if got := c.SafeURL(); got != "nats://localhost:4222" {
		t.Fatalf("SafeURL() = %q", got)
	}
	raw := "dial nats://private%2Duser:p%40ss%2Fword@localhost:4222 private-user p@ss/word token-value secret-value /private/user.creds /private/client.key"
	got := c.Redact(raw)
	for _, secret := range []string{"private%2Duser", "private-user", "p%40ss%2Fword", "p@ss/word", "token-value", "secret-value", "/private/user.creds", "/private/client.key"} {
		if strings.Contains(got, secret) {
			t.Errorf("redacted text leaked %q: %q", secret, got)
		}
	}
	if !strings.Contains(got, "localhost:4222") {
		t.Errorf("redaction lost endpoint: %q", got)
	}
	if safe := (Connection{URL: "nats://secret@host:%"}).SafeURL(); strings.Contains(safe, "secret") {
		t.Errorf("invalid URL leaked credential: %q", safe)
	}
}

func TestValidationErrorsDoNotLeakSecrets(t *testing.T) {
	isolatedConfig(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	writeConfig(t, path, "connections:\n  - name: test\n    url: nats://ultrasecret@host:bad\n    token: othersecret")
	_, err := Load(path, "", "")
	if err == nil {
		t.Fatal("invalid config accepted")
	}
	if strings.Contains(err.Error(), "ultrasecret") || strings.Contains(err.Error(), "othersecret") {
		t.Fatalf("configuration error leaked authentication: %v", err)
	}
}

func TestRedactEscapedCredentials(t *testing.T) {
	secret := "p$ss@word/with space"
	c := Connection{URL: "nats://localhost", Token: secret}
	for _, escaped := range []string{
		url.QueryEscape(secret),
		url.PathEscape(secret),
		url.User(secret).String(),
		strings.ReplaceAll(url.QueryEscape(secret), "%2F", "%2f"),
	} {
		if got := c.Redact("authorization failed: " + escaped); strings.Contains(got, escaped) {
			t.Errorf("encoded credential leaked: %q", got)
		}
	}
	if got := (Connection{URL: "secret@localhost"}).SafeURL(); strings.Contains(got, "secret") {
		t.Errorf("malformed URL leaked a credential: %q", got)
	}
}

func TestConnectionRejectsMalformedHosts(t *testing.T) {
	for _, endpoint := range []string{"nats://::1", "nats://[not-ip]:4222", "nats://host:4222\n"} {
		if err := (Connection{URL: endpoint}).Validate(); err == nil {
			t.Errorf("accepted malformed host: %q", endpoint)
		}
	}
	if err := (Connection{URL: "nats://[fe80::1%25en0]:4222"}).Validate(); err != nil {
		t.Errorf("valid IPv6 zone rejected: %v", err)
	}
}
