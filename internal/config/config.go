// Package config loads and validates named NATS connections.
package config

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Refresh     time.Duration
	Connections []Connection
}

type Connection struct {
	Name        string `yaml:"name"`
	URL         string `yaml:"url"`
	Credentials string `yaml:"credentials,omitempty"`
	Token       string `yaml:"token,omitempty"`
	User        string `yaml:"user,omitempty"`
	Password    string `yaml:"password,omitempty"`
	TLSCA       string `yaml:"tls_ca,omitempty"`
	TLSCert     string `yaml:"tls_cert,omitempty"`
	TLSKey      string `yaml:"tls_key,omitempty"`
	Domain      string `yaml:"domain,omitempty"`
}

// Load applies server, file, environment, and localhost defaults in that order.
// A server override bypasses config files completely. Refresh overrides the file.
// A directory config path merges every *.yaml/*.yml file directly inside it.
func Load(path, server, refresh string) (Config, error) {
	var disk struct {
		Refresh     string       `yaml:"refresh"`
		Connections []Connection `yaml:"connections"`
	}
	base := "."
	if server != "" {
		disk.Connections = []Connection{{Name: "default", URL: server}}
	} else {
		explicit := path != ""
		if !explicit {
			dir, err := os.UserConfigDir()
			if err != nil {
				return Config{}, errors.New("cannot locate the default config directory")
			}
			path = filepath.Join(dir, "natop", "config.yaml")
		}
		var err error
		path, err = resolvePath(path, ".")
		if err != nil {
			return Config{}, errors.New("cannot resolve config path")
		}
		if info, statErr := os.Stat(path); statErr == nil && info.IsDir() {
			conns, dirRefresh, err := loadDirectory(path, refresh)
			if err != nil {
				return Config{}, err
			}
			disk.Connections, disk.Refresh = conns, dirRefresh
		} else {
			f, err := os.Open(path)
			if err != nil {
				if explicit || !errors.Is(err, os.ErrNotExist) {
					return Config{}, errors.New("cannot read config file")
				}
				server = os.Getenv("NATS_URL")
				if server == "" {
					server = "nats://localhost:4222"
				}
				disk.Connections = []Connection{{Name: "default", URL: server}}
			} else {
				defer f.Close()
				decoder := yaml.NewDecoder(f)
				decoder.KnownFields(true)
				if err := decoder.Decode(&disk); err != nil {
					if err == io.EOF {
						return Config{}, errors.New("config file is empty")
					}
					return Config{}, errors.New("invalid config YAML: check field names and value types")
				}
				if decoder.Decode(new(any)) != io.EOF {
					return Config{}, errors.New("invalid config YAML: trailing content")
				}
				base = filepath.Dir(path)
			}
		}
	}
	if refresh == "" {
		refresh = disk.Refresh
	}
	if refresh == "" {
		refresh = "2s"
	}
	interval, err := time.ParseDuration(refresh)
	if err != nil || interval < 250*time.Millisecond || interval > time.Hour {
		return Config{}, errors.New("refresh must be a duration between 250ms and 1h")
	}
	if len(disk.Connections) == 0 {
		return Config{}, errors.New("config must contain at least one connection")
	}
	if err := validateConnections(disk.Connections, base, make(map[string]string), ""); err != nil {
		return Config{}, err
	}
	return Config{Refresh: interval, Connections: disk.Connections}, nil
}

// loadDirectory merges every *.yaml/*.yml file directly inside dir (not
// recursive), sorted by filename for determinism. Each file's relative paths
// resolve against that file's own directory. cliRefresh is the --refresh flag;
// when it is empty, files must agree on any refresh they set.
func loadDirectory(dir, cliRefresh string) ([]Connection, string, error) {
	yamls, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return nil, "", errors.New("cannot list config directory")
	}
	ymls, err := filepath.Glob(filepath.Join(dir, "*.yml"))
	if err != nil {
		return nil, "", errors.New("cannot list config directory")
	}
	files := append(yamls, ymls...)
	sort.Strings(files)
	if len(files) == 0 {
		return nil, "", errors.New("no config files found in directory")
	}
	var conns []Connection
	var refresh, refreshFile string
	origins := make(map[string]string)
	for _, path := range files {
		name := filepath.Base(path)
		fileRefresh, fileConns, err := decodeConnectionsFile(path)
		if err != nil {
			return nil, "", fmt.Errorf("%s: %w", name, err)
		}
		if err := validateConnections(fileConns, filepath.Dir(path), origins, name); err != nil {
			return nil, "", fmt.Errorf("%s: %w", name, err)
		}
		if fileRefresh != "" && cliRefresh == "" {
			if refresh != "" && refresh != fileRefresh {
				return nil, "", fmt.Errorf("conflicting refresh values: %s sets %q, %s sets %q; use --refresh to override", refreshFile, refresh, name, fileRefresh)
			}
			refresh, refreshFile = fileRefresh, name
		}
		conns = append(conns, fileConns...)
	}
	return conns, refresh, nil
}

// decodeConnectionsFile reads one directory member file. Unlike the top-level
// config, a missing file here is always an error: files come from a glob of
// files known to exist.
func decodeConnectionsFile(path string) (refresh string, conns []Connection, err error) {
	var disk struct {
		Refresh     string       `yaml:"refresh"`
		Connections []Connection `yaml:"connections"`
	}
	f, err := os.Open(path)
	if err != nil {
		return "", nil, errors.New("cannot read config file")
	}
	defer f.Close()
	decoder := yaml.NewDecoder(f)
	decoder.KnownFields(true)
	if err := decoder.Decode(&disk); err != nil {
		if err == io.EOF {
			return "", nil, errors.New("config file is empty")
		}
		return "", nil, errors.New("invalid config YAML: check field names and value types")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return "", nil, errors.New("invalid config YAML: trailing content")
	}
	return disk.Refresh, disk.Connections, nil
}

// validateConnections expands, validates, and enforces name uniqueness for
// conns, resolving relative paths against base. origins records each name's
// source file so cross-file duplicates can be reported; file identifies conns'
// own source and is empty for a single config file, which keeps today's plain
// "duplicate name" wording for that case.
func validateConnections(conns []Connection, base string, origins map[string]string, file string) error {
	for i := range conns {
		c := &conns[i]
		if strings.TrimSpace(c.Name) == "" {
			return fmt.Errorf("connection %d: name is required", i+1)
		}
		if origin, dup := origins[c.Name]; dup {
			if file == "" {
				return fmt.Errorf("connection %d: duplicate name", i+1)
			}
			return fmt.Errorf("connection %d: duplicate name %q (already defined in %s)", i+1, c.Name, origin)
		}
		origins[c.Name] = file
		if err := c.expand(base); err != nil {
			return fmt.Errorf("connection %d: %w", i+1, err)
		}
		if err := c.Validate(); err != nil {
			return fmt.Errorf("connection %d: %w", i+1, err)
		}
	}
	return nil
}

// Validate checks endpoint, authentication, and TLS settings without connecting.
// Names are checked by Load because they identify entries within a config file.
func (c Connection) Validate() error {
	u, err := url.Parse(c.URL)
	if err != nil || u.Opaque != "" || u.Hostname() == "" || strings.ContainsAny(u.Host, ", \t\r\n") {
		return errors.New("URL must contain one valid NATS server endpoint")
	}
	if strings.Count(u.Host, ":") > 1 && !strings.Contains(u.Host, "[") {
		return errors.New("URL must contain one valid NATS server endpoint")
	}
	switch u.Scheme {
	case "nats", "tls", "ws", "wss":
	default:
		return errors.New("URL scheme must be nats, tls, ws, or wss")
	}
	if u.RawQuery != "" || u.ForceQuery || strings.Contains(c.URL, "#") || ((u.Scheme == "nats" || u.Scheme == "tls") && u.Path != "") {
		return errors.New("URL cannot contain a query or fragment; paths require ws or wss")
	}
	if strings.HasSuffix(u.Host, ":") {
		return errors.New("URL port must be between 1 and 65535")
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return errors.New("URL port must be between 1 and 65535")
		}
	}
	methods := 0
	for _, enabled := range []bool{u.User != nil, c.Credentials != "", c.Token != "", c.User != ""} {
		if enabled {
			methods++
		}
	}
	if methods > 1 {
		return errors.New("use only one authentication method: URL credentials, credentials file, token, or user/password")
	}
	if c.Password != "" && c.User == "" {
		return errors.New("password requires user")
	}
	if u.User != nil && u.User.Username() == "" {
		return errors.New("URL authentication requires a nonempty user or token")
	}
	if (c.TLSCert == "") != (c.TLSKey == "") {
		return errors.New("tls_cert and tls_key must be specified together")
	}
	if strings.ContainsAny(c.Domain, ".*> \t\r\n") {
		return errors.New("domain must be a single NATS subject token without wildcards")
	}
	return nil
}

// SafeURL returns a displayable endpoint with all URL user information removed.
func (c Connection) SafeURL() string {
	u, err := url.Parse(c.URL)
	if err != nil || u.Host == "" || u.Opaque != "" {
		return "[invalid URL]"
	}
	u.User = nil
	return u.String()
}

// Redact removes configured secrets, including escaped URL credentials, from
// diagnostics before they are shown in the terminal.
func (c Connection) Redact(s string) string {
	if c.URL != "" {
		s = strings.ReplaceAll(s, c.URL, c.SafeURL())
	}
	// Escaped octets are case insensitive; diagnostics may render either form.
	s = normalizeEscapes(s)
	secrets := []string{c.Credentials, c.Token, c.User, c.Password, c.TLSCA, c.TLSCert, c.TLSKey}
	// Read the authority directly as malformed URLs can still contain secrets.
	if _, rest, ok := strings.Cut(c.URL, "://"); ok {
		authority := strings.SplitN(rest, "/", 2)[0]
		if at := strings.LastIndex(authority, "@"); at >= 0 {
			for _, part := range strings.SplitN(authority[:at], ":", 2) {
				secrets = append(secrets, part)
				if decoded, err := url.PathUnescape(part); err == nil {
					secrets = append(secrets, decoded)
				}
			}
		}
	}
	var variants []string
	for _, secret := range secrets {
		if secret != "" {
			variants = append(variants, secret, url.PathEscape(secret), url.QueryEscape(secret), url.User(secret).String())
		}
	}
	// Longer forms must be replaced first so a short secret cannot leave a suffix.
	sort.Slice(variants, func(i, j int) bool { return len(variants[i]) > len(variants[j]) })
	for _, secret := range variants {
		s = strings.ReplaceAll(s, normalizeEscapes(secret), "[redacted]")
	}
	return s
}

var environmentReference = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)
var escapedOctet = regexp.MustCompile(`%[0-9a-fA-F]{2}`)

func normalizeEscapes(s string) string {
	return escapedOctet.ReplaceAllStringFunc(s, strings.ToUpper)
}

func (c *Connection) expand(base string) error {
	for _, field := range []struct {
		name  string
		value *string
		path  bool
	}{
		{"url", &c.URL, false},
		{"credentials", &c.Credentials, true},
		{"token", &c.Token, false},
		{"user", &c.User, false},
		{"password", &c.Password, false},
		{"tls_ca", &c.TLSCA, true},
		{"tls_cert", &c.TLSCert, true},
		{"tls_key", &c.TLSKey, true},
	} {
		missing := false
		*field.value = environmentReference.ReplaceAllStringFunc(*field.value, func(reference string) string {
			value, exists := os.LookupEnv(reference[2 : len(reference)-1])
			missing = missing || !exists
			return value
		})
		if missing {
			return fmt.Errorf("%s references an unset environment variable", field.name)
		}
		if field.path && *field.value != "" {
			resolved, err := resolvePath(*field.value, base)
			if err != nil {
				return fmt.Errorf("cannot resolve %s path", field.name)
			}
			*field.value = resolved
		}
	}
	return nil
}

func resolvePath(path, base string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if path == "~" {
			path = home
		} else {
			path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(base, path)
	}
	return filepath.Abs(path)
}
