package config

import (
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// baseEnv returns a valid production-like environment rooted in a temp dir.
func baseEnv(t *testing.T) map[string]string {
	t.Helper()
	dir := t.TempDir()
	return map[string]string{
		"HOMEDRIVE_PUBLIC_URL": "https://drive.example.com",
		"HOMEDRIVE_STATE_DIR":  filepath.Join(dir, "state"),
		"HOMEDRIVE_DATA_PATH":  filepath.Join(dir, "files"),
		"HOMEDRIVE_WEB_DIR":    filepath.Join(dir, "web"),
		"HOMEDRIVE_SMTP_HOST":  "smtp.example.com",
		"HOMEDRIVE_SMTP_FROM":  "Homedrive <drive@example.com>",
	}
}

func environ(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(environ(baseEnv(t)))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Origin(); got != "https://drive.example.com" {
		t.Errorf("Origin() = %q", got)
	}
	if cfg.Listen != ":8080" || cfg.BackupPath != "" || cfg.BackupRetentionDays != 30 {
		t.Errorf("unexpected defaults: %+v", cfg)
	}
	if cfg.BackupTime != (TimeOfDay{3, 0}) {
		t.Errorf("BackupTime = %v", cfg.BackupTime)
	}
	if cfg.SMTP.Port != 587 || cfg.SMTP.Security != SMTPStartTLS || cfg.SMTP.From.Address != "drive@example.com" {
		t.Errorf("SMTP = %+v", cfg.SMTP)
	}
	if !cfg.HTTPS() {
		t.Error("HTTPS() = false")
	}
	want := netip.MustParseAddr("172.30.33.4") // Home Assistant add-on network
	trusted := false
	for _, p := range cfg.TrustedProxies {
		trusted = trusted || p.Contains(want)
	}
	if !trusted {
		t.Errorf("default trusted proxies %v don't cover %v", cfg.TrustedProxies, want)
	}
}

func TestOptionsFileAndEnvPrecedence(t *testing.T) {
	env := baseEnv(t)
	optsPath := filepath.Join(t.TempDir(), "options.json")
	opts := `{
		"public_url": "https://from-options.example.com",
		"backup_retention_days": 14,
		"smtp_port": 465,
		"smtp_security": "tls",
		"trusted_proxies": ["172.30.32.0/23", "10.1.2.3"],
		"made_up": true
	}`
	if err := os.WriteFile(optsPath, []byte(opts), 0o600); err != nil {
		t.Fatal(err)
	}
	env["HOMEDRIVE_OPTIONS_FILE"] = optsPath
	delete(env, "HOMEDRIVE_PUBLIC_URL")
	env["HOMEDRIVE_BACKUP_RETENTION_DAYS"] = "60" // environment wins
	env["HOMEDRIVE_DATAPATH"] = "/typo"

	cfg, err := Load(environ(env))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Origin() != "https://from-options.example.com" {
		t.Errorf("public_url from options file not used: %s", cfg.Origin())
	}
	if cfg.BackupRetentionDays != 60 {
		t.Errorf("env should override options file, got %d", cfg.BackupRetentionDays)
	}
	if cfg.SMTP.Port != 465 || cfg.SMTP.Security != SMTPTLS {
		t.Errorf("SMTP = %+v", cfg.SMTP)
	}
	if len(cfg.TrustedProxies) != 2 || cfg.TrustedProxies[1] != netip.MustParsePrefix("10.1.2.3/32") {
		t.Errorf("TrustedProxies = %v", cfg.TrustedProxies)
	}
	joined := strings.Join(cfg.Warnings, "\n")
	if !strings.Contains(joined, `"made_up"`) || !strings.Contains(joined, "HOMEDRIVE_DATAPATH") {
		t.Errorf("expected warnings for unknown option and env var, got %q", cfg.Warnings)
	}
}

func TestSMTPPasswordFile(t *testing.T) {
	env := baseEnv(t)
	secret := filepath.Join(t.TempDir(), "smtp_password")
	if err := os.WriteFile(secret, []byte("s3cret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env["HOMEDRIVE_SMTP_USERNAME"] = "drive"
	env["HOMEDRIVE_SMTP_PASSWORD_FILE"] = secret

	cfg, err := Load(environ(env))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SMTP.Password != "s3cret" {
		t.Errorf("password = %q", cfg.SMTP.Password)
	}
	for _, v := range cfg.LogAttrs() {
		if s, ok := v.(string); ok && strings.Contains(s, "s3cret") {
			t.Error("LogAttrs leaks the SMTP password")
		}
	}

	env["HOMEDRIVE_SMTP_PASSWORD"] = "other"
	if _, err := Load(environ(env)); err == nil {
		t.Error("expected an error when both password and password file are set")
	}
}

func TestPublicURL(t *testing.T) {
	tests := []struct {
		in, want string
		ok       bool
	}{
		{"https://drive.example.com", "https://drive.example.com", true},
		{"https://Drive.Example.com:443/", "https://drive.example.com", true},
		{"https://drive.example.com:8443", "https://drive.example.com:8443", true},
		{"http://localhost:8080", "http://localhost:8080", true},
		{"http://127.0.0.1:8080", "http://127.0.0.1:8080", true},
		{"http://drive.example.com", "", false},
		{"https://drive.example.com/homedrive", "", false},
		{"https://drive.example.com/?x=1", "", false},
		{"https://user:pw@drive.example.com", "", false},
		{"drive.example.com", "", false},
		{"ftp://drive.example.com", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		u, err := parsePublicURL(tt.in)
		if (err == nil) != tt.ok {
			t.Errorf("parsePublicURL(%q) error = %v, want ok=%v", tt.in, err, tt.ok)
			continue
		}
		if tt.ok && u.String() != tt.want {
			t.Errorf("parsePublicURL(%q) = %q, want %q", tt.in, u.String(), tt.want)
		}
	}
}

func TestMatchesHost(t *testing.T) {
	cfg, err := Load(environ(baseEnv(t)))
	if err != nil {
		t.Fatal(err)
	}
	for host, want := range map[string]bool{
		"drive.example.com":      true,
		"DRIVE.example.com":      true,
		"drive.example.com:443":  true,
		"drive.example.com:8080": false,
		"192.168.1.10:8080":      false,
		"evil.example.com":       false,
		"drive.example.com.evil": false,
		"":                       false,
	} {
		if got := cfg.MatchesHost(host); got != want {
			t.Errorf("MatchesHost(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestValidationErrors(t *testing.T) {
	tests := map[string]struct {
		set     map[string]string
		wantErr string
	}{
		"smtp required in production": {map[string]string{"HOMEDRIVE_SMTP_HOST": ""}, "smtp_host: is required"},
		"relative data path":          {map[string]string{"HOMEDRIVE_DATA_PATH": "files"}, "data_path: must be an absolute path"},
		"backup inside data": {
			map[string]string{"HOMEDRIVE_DATA_PATH": filepath.Join(os.TempDir(), "hd"), "HOMEDRIVE_BACKUP_PATH": filepath.Join(os.TempDir(), "hd", "backup")},
			"backup_path: must not be the same as, or inside, data_path",
		},
		"data equals state": {
			map[string]string{"HOMEDRIVE_DATA_PATH": filepath.Join(os.TempDir(), "same"), "HOMEDRIVE_STATE_DIR": filepath.Join(os.TempDir(), "same")},
			"data_path: must not be the same as, or inside, state_dir",
		},
		"bad backup time":     {map[string]string{"HOMEDRIVE_BACKUP_TIME": "24:00"}, "backup_time"},
		"bad retention":       {map[string]string{"HOMEDRIVE_BACKUP_RETENTION_DAYS": "0"}, "backup_retention_days"},
		"plain smtp":          {map[string]string{"HOMEDRIVE_SMTP_SECURITY": "none"}, "smtp_security"},
		"username alone":      {map[string]string{"HOMEDRIVE_SMTP_USERNAME": "drive"}, "smtp_username"},
		"bad from":            {map[string]string{"HOMEDRIVE_SMTP_FROM": "not an address"}, "smtp_from"},
		"bad proxy":           {map[string]string{"HOMEDRIVE_TRUSTED_PROXIES": "10.0.0.0/33"}, "trusted_proxies"},
		"bad log level":       {map[string]string{"HOMEDRIVE_LOG_LEVEL": "loud"}, "log_level"},
		"bad listen":          {map[string]string{"HOMEDRIVE_LISTEN": "8080"}, "listen"},
		"http on public host": {map[string]string{"HOMEDRIVE_PUBLIC_URL": "http://drive.example.com"}, "must use https"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			env := baseEnv(t)
			for k, v := range tt.set {
				env[k] = v
			}
			_, err := Load(environ(env))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestSMTPOptionalOnLocalhost(t *testing.T) {
	env := baseEnv(t)
	env["HOMEDRIVE_PUBLIC_URL"] = "http://localhost:8080"
	delete(env, "HOMEDRIVE_SMTP_HOST")
	delete(env, "HOMEDRIVE_SMTP_FROM")
	cfg, err := Load(environ(env))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SMTP.Configured() || cfg.HTTPS() {
		t.Errorf("SMTP configured = %v, HTTPS = %v", cfg.SMTP.Configured(), cfg.HTTPS())
	}
}

func TestErrorsAreReportedTogether(t *testing.T) {
	env := baseEnv(t)
	env["HOMEDRIVE_PUBLIC_URL"] = ""
	env["HOMEDRIVE_LOG_LEVEL"] = "loud"
	_, err := Load(environ(env))
	if err == nil || !strings.Contains(err.Error(), "public_url") || !strings.Contains(err.Error(), "log_level") {
		t.Errorf("want both problems in one error, got %v", err)
	}
}
