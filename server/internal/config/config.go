// Package config loads Homedrive's settings and validates them.
//
// Settings come from three layers, later ones winning: built-in defaults, the
// Home Assistant add-on options file (HOMEDRIVE_OPTIONS_FILE, a JSON object)
// and HOMEDRIVE_* environment variables. Option names are the same in both
// places: smtp_host in the options file is HOMEDRIVE_SMTP_HOST in the
// environment.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/mail"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

const envPrefix = "HOMEDRIVE_"

// These point at files, so they only exist as environment variables.
const (
	envOptionsFile      = envPrefix + "OPTIONS_FILE"
	envSMTPPasswordFile = envPrefix + "SMTP_PASSWORD_FILE"
)

// defaults lists every option Homedrive understands.
var defaults = map[string]string{
	"public_url":            "",
	"listen":                ":8080",
	"state_dir":             "/config",
	"data_path":             "/data",
	"backup_path":           "",
	"backup_time":           "03:00",
	"backup_retention_days": "30",
	"smtp_host":             "",
	"smtp_port":             "587",
	"smtp_security":         string(SMTPStartTLS),
	"smtp_username":         "",
	"smtp_password":         "",
	"smtp_from":             "",
	"trusted_proxies":       "127.0.0.0/8, ::1/128, 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, fc00::/7",
	"web_dir":               "/app/web",
	"log_level":             "info",
}

type SMTPSecurity string

const (
	SMTPStartTLS SMTPSecurity = "starttls"
	SMTPTLS      SMTPSecurity = "tls"
)

type SMTP struct {
	Host     string
	Port     int
	Security SMTPSecurity
	Username string
	Password string
	From     *mail.Address
}

// Configured reports whether outgoing mail is set up. It can only be off when
// public_url is a localhost address (development).
func (s SMTP) Configured() bool { return s.Host != "" }

type TimeOfDay struct{ Hour, Minute int }

func (t TimeOfDay) String() string { return fmt.Sprintf("%02d:%02d", t.Hour, t.Minute) }

type Config struct {
	// PublicURL is the origin users reach Homedrive on, normalised to scheme
	// and host only (no default port, no trailing slash).
	PublicURL           *url.URL
	Listen              string
	StateDir            string
	DataPath            string
	BackupPath          string // empty: backups off
	BackupTime          TimeOfDay
	BackupRetentionDays int
	SMTP                SMTP
	TrustedProxies      []netip.Prefix
	WebDir              string
	LogLevel            slog.Level

	// Warnings are non-fatal problems found while loading, such as unknown
	// options. They are meant to be logged once at startup.
	Warnings []string
}

// Load builds the configuration from environ (as returned by os.Environ) and
// the options file it may point to. All validation problems are reported
// together.
func Load(environ []string) (*Config, error) {
	env := make(map[string]string)
	for _, kv := range environ {
		if k, v, ok := strings.Cut(kv, "="); ok && strings.HasPrefix(k, envPrefix) {
			env[k] = v
		}
	}

	values := make(map[string]string, len(defaults))
	for k, v := range defaults {
		values[k] = v
	}
	var warnings []string

	if path := env[envOptionsFile]; path != "" {
		opts, err := readOptionsFile(path)
		if err != nil {
			return nil, err
		}
		for k, v := range opts {
			if _, known := defaults[k]; !known {
				warnings = append(warnings, fmt.Sprintf("ignoring unknown option %q in %s", k, path))
				continue
			}
			values[k] = v
		}
	}

	for name, v := range env {
		if name == envOptionsFile || name == envSMTPPasswordFile {
			continue
		}
		key := strings.ToLower(strings.TrimPrefix(name, envPrefix))
		if _, known := defaults[key]; !known {
			warnings = append(warnings, fmt.Sprintf("ignoring unknown environment variable %s", name))
			continue
		}
		values[key] = v
	}

	if path := env[envSMTPPasswordFile]; path != "" {
		if values["smtp_password"] != "" {
			return nil, fmt.Errorf("set either smtp_password or %s, not both", envSMTPPasswordFile)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", envSMTPPasswordFile, err)
		}
		values["smtp_password"] = strings.TrimRight(string(b), "\r\n")
	}

	cfg, err := parse(values)
	if err != nil {
		return nil, err
	}
	slices.Sort(warnings)
	cfg.Warnings = warnings
	return cfg, nil
}

// readOptionsFile reads the add-on options as strings, so that they go
// through the same parsing as environment variables.
func readOptionsFile(path string) (map[string]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading options file: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var raw map[string]any
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("reading options file %s: %w", path, err)
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		s, err := optionString(v)
		if err != nil {
			return nil, fmt.Errorf("options file %s: %s: %w", path, k, err)
		}
		out[k] = s
	}
	return out, nil
}

func optionString(v any) (string, error) {
	switch v := v.(type) {
	case nil:
		return "", nil
	case string:
		return v, nil
	case json.Number:
		return v.String(), nil
	case bool:
		return strconv.FormatBool(v), nil
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			s, err := optionString(item)
			if err != nil {
				return "", err
			}
			parts = append(parts, s)
		}
		return strings.Join(parts, ", "), nil
	default:
		return "", fmt.Errorf("unsupported value type %T", v)
	}
}

var timeOfDay = regexp.MustCompile(`^([01]\d|2[0-3]):([0-5]\d)$`)

func parse(values map[string]string) (*Config, error) {
	cfg := &Config{}
	var errs []error
	bad := func(key, msg string) { errs = append(errs, fmt.Errorf("%s: %s", key, msg)) }

	if u, err := parsePublicURL(values["public_url"]); err != nil {
		bad("public_url", err.Error())
	} else {
		cfg.PublicURL = u
	}

	if _, _, err := net.SplitHostPort(values["listen"]); err != nil {
		bad("listen", "must be host:port, for example :8080")
	} else {
		cfg.Listen = values["listen"]
	}

	absPath := func(key string, required bool) string {
		v := strings.TrimSpace(values[key])
		switch {
		case v == "" && required:
			bad(key, "is required")
		case v == "":
		case !filepath.IsAbs(v):
			bad(key, "must be an absolute path")
		default:
			return filepath.Clean(v)
		}
		return ""
	}
	cfg.StateDir = absPath("state_dir", true)
	cfg.DataPath = absPath("data_path", true)
	cfg.BackupPath = absPath("backup_path", false)
	cfg.WebDir = absPath("web_dir", true)

	dirs := []struct{ key, path string }{
		{"state_dir", cfg.StateDir}, {"data_path", cfg.DataPath}, {"backup_path", cfg.BackupPath},
	}
	for i, a := range dirs {
		for _, b := range dirs[i+1:] {
			if a.path != "" && b.path != "" && overlaps(a.path, b.path) {
				bad(b.key, fmt.Sprintf("must not be the same as, or inside, %s (%s)", a.key, a.path))
			}
		}
	}

	if m := timeOfDay.FindStringSubmatch(values["backup_time"]); m == nil {
		bad("backup_time", "must be HH:MM in 24-hour time, for example 03:00")
	} else {
		hour, _ := strconv.Atoi(m[1])
		minute, _ := strconv.Atoi(m[2])
		cfg.BackupTime = TimeOfDay{hour, minute}
	}

	if n, err := strconv.Atoi(values["backup_retention_days"]); err != nil || n < 1 || n > 365 {
		bad("backup_retention_days", "must be a whole number from 1 to 365")
	} else {
		cfg.BackupRetentionDays = n
	}

	cfg.SMTP = parseSMTP(values, cfg.PublicURL, bad)

	if prefixes, err := parsePrefixes(values["trusted_proxies"]); err != nil {
		bad("trusted_proxies", err.Error())
	} else {
		cfg.TrustedProxies = prefixes
	}

	if err := cfg.LogLevel.UnmarshalText([]byte(values["log_level"])); err != nil {
		bad("log_level", "must be debug, info, warn or error")
	}

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return cfg, nil
}

func parsePublicURL(v string) (*url.URL, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil, errors.New("is required, for example https://drive.example.com")
	}
	u, err := url.Parse(v)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, errors.New("must be an absolute http(s) URL, for example https://drive.example.com")
	}
	if u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("must be only scheme and host, without path, query or credentials")
	}
	host := strings.ToLower(u.Hostname())
	if u.Scheme == "http" && !isLoopbackHost(host) {
		return nil, errors.New("must use https (plain http is only allowed for localhost)")
	}
	return &url.URL{Scheme: u.Scheme, Host: canonicalHost(u.Scheme, u.Host)}, nil
}

// canonicalHost lower-cases a Host value and drops the scheme's default port,
// so that "Drive.Example.com:443" and "drive.example.com" compare equal.
func canonicalHost(scheme, hostport string) string {
	hostport = strings.ToLower(hostport)
	host, port, err := net.SplitHostPort(hostport)
	if err != nil {
		return hostport // no port
	}
	if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		if strings.Contains(host, ":") {
			return "[" + host + "]"
		}
		return host
	}
	return hostport
}

func isLoopbackHost(host string) bool {
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip, err := netip.ParseAddr(strings.Trim(host, "[]"))
	return err == nil && ip.IsLoopback()
}

func overlaps(a, b string) bool {
	within := func(child, parent string) bool {
		rel, err := filepath.Rel(parent, child)
		return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
	}
	return within(a, b) || within(b, a)
}

func parseSMTP(values map[string]string, publicURL *url.URL, bad func(key, msg string)) SMTP {
	s := SMTP{
		Host:     strings.TrimSpace(values["smtp_host"]),
		Username: values["smtp_username"],
		Password: values["smtp_password"],
	}
	if s.Host == "" {
		if publicURL != nil && !isLoopbackHost(publicURL.Hostname()) {
			bad("smtp_host", "is required: login codes are sent by email")
		}
		return s
	}
	if strings.ContainsFunc(s.Host, unicode.IsSpace) || strings.Contains(s.Host, "/") {
		bad("smtp_host", "must be a host name or IP address")
	}
	if n, err := strconv.Atoi(values["smtp_port"]); err != nil || n < 1 || n > 65535 {
		bad("smtp_port", "must be a port number")
	} else {
		s.Port = n
	}
	switch sec := SMTPSecurity(strings.ToLower(values["smtp_security"])); sec {
	case SMTPStartTLS, SMTPTLS:
		s.Security = sec
	default:
		bad("smtp_security", "must be starttls or tls (unencrypted SMTP is not supported)")
	}
	if (s.Username == "") != (s.Password == "") {
		bad("smtp_username", "and smtp_password must be set together")
	}
	if from := strings.TrimSpace(values["smtp_from"]); from == "" {
		bad("smtp_from", "is required when smtp_host is set")
	} else if addr, err := mail.ParseAddress(from); err != nil {
		bad("smtp_from", "must be an email address, for example Homedrive <drive@example.com>")
	} else {
		s.From = addr
	}
	return s
}

func parsePrefixes(v string) ([]netip.Prefix, error) {
	fields := strings.FieldsFunc(v, func(r rune) bool { return r == ',' || unicode.IsSpace(r) })
	prefixes := make([]netip.Prefix, 0, len(fields))
	for _, f := range fields {
		if strings.Contains(f, "/") {
			p, err := netip.ParsePrefix(f)
			if err != nil {
				return nil, fmt.Errorf("%q is not a valid CIDR range", f)
			}
			prefixes = append(prefixes, p.Masked())
			continue
		}
		ip, err := netip.ParseAddr(f)
		if err != nil {
			return nil, fmt.Errorf("%q is not a valid IP address or CIDR range", f)
		}
		prefixes = append(prefixes, netip.PrefixFrom(ip.Unmap(), ip.Unmap().BitLen()))
	}
	return prefixes, nil
}

// HTTPS reports whether users reach Homedrive over HTTPS (always true outside
// development).
func (c *Config) HTTPS() bool { return c.PublicURL.Scheme == "https" }

// Origin is the public origin, for example https://drive.example.com.
func (c *Config) Origin() string { return c.PublicURL.String() }

// MatchesHost reports whether a request's Host header names the public URL.
func (c *Config) MatchesHost(host string) bool {
	return canonicalHost(c.PublicURL.Scheme, host) == c.PublicURL.Host
}

// LogAttrs describes the configuration for the startup log, without secrets.
func (c *Config) LogAttrs() []any {
	backup := "off"
	if c.BackupPath != "" {
		backup = fmt.Sprintf("%s daily at %s, keep %d days", c.BackupPath, c.BackupTime, c.BackupRetentionDays)
	}
	mail := "not configured (codes are logged, development only)"
	if c.SMTP.Configured() {
		mail = fmt.Sprintf("%s:%d (%s) from %s", c.SMTP.Host, c.SMTP.Port, c.SMTP.Security, c.SMTP.From.Address)
	}
	return []any{
		"public_url", c.Origin(),
		"listen", c.Listen,
		"state_dir", c.StateDir,
		"data_path", c.DataPath,
		"backup", backup,
		"smtp", mail,
		"trusted_proxies", c.TrustedProxies,
	}
}
