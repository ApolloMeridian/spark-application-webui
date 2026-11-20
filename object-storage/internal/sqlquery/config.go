package sqlquery

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Profile struct {
	KyuubiUser string
}

type BootstrapUser struct {
	Username string `json:"username"`
	Tenant   string `json:"tenantId"`
}

type Config struct {
	Database, Kyuubi, Polaris, Realm, Warehouse, CAFile           string
	SigningKey, StorageURL, PolarisID, PolarisSecret, PolarisRole string
	Bootstrap                                                     []BootstrapUser
	MaxRows, MaxBytes, Workers                                    int
	Timeout, Retention, HistoryRetention, AuditRetention          time.Duration
}

var safeName = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,127}$`)
var catalogName = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,127}$`)
var userName = regexp.MustCompile(`^[a-zA-Z0-9_.-]{1,128}$`)
var icebergMetadataTables = map[string]bool{
	"entries": true, "files": true, "data_files": true, "delete_files": true, "history": true,
	"snapshots": true, "manifests": true, "partitions": true, "refs": true, "all_data_files": true,
	"all_delete_files": true, "all_files": true, "all_manifests": true, "all_entries": true,
	"position_deletes": true, "metadata_log_entries": true,
}

func LoadConfig() (Config, error) {
	env := os.Getenv
	cfg := Config{
		SigningKey: env("SQL_SIGNING_KEY"), StorageURL: strings.TrimRight(env("SQL_STORAGE_URL"), "/"),
		Kyuubi: strings.TrimRight(env("SQL_KYUUBI_URL"), "/"), Polaris: strings.TrimRight(env("SQL_POLARIS_URL"), "/"),
		Realm: env("SQL_POLARIS_REALM"), Warehouse: env("SQL_POLARIS_WAREHOUSE"), CAFile: env("SQL_CA_FILE"),
		PolarisID: env("SQL_POLARIS_CLIENT_ID"), PolarisSecret: env("SQL_POLARIS_CLIENT_SECRET"), PolarisRole: env("SQL_POLARIS_ROLE"),
	}
	host, user, password := env("DATABASE_HOST"), env("DATABASE_USERNAME"), env("DATABASE_PASSWORD")
	name, port, ssl := env("DATABASE_NAME"), env("DATABASE_PORT"), env("DATABASE_SSLMODE")
	if host == "" || user == "" || password == "" || name == "" || port == "" || ssl == "" {
		return cfg, errors.New("SQL requires the core database configuration")
	}
	dsn := url.URL{Scheme: "postgres", Host: net.JoinHostPort(host, port), User: url.UserPassword(user, password), Path: "/" + name, RawQuery: url.Values{"sslmode": {ssl}}.Encode()}
	cfg.Database = dsn.String()
	if len(cfg.SigningKey) < 32 || !userName.MatchString(cfg.Warehouse) ||
		!userName.MatchString(cfg.PolarisID) || strings.Contains(strings.ToLower(cfg.PolarisID), "root") ||
		cfg.PolarisSecret == "" || !userName.MatchString(cfg.PolarisRole) || strings.EqualFold(cfg.PolarisRole, "all") {
		return cfg, errors.New("SQL requires a signing key and a scoped Polaris service principal")
	}
	store, err := url.Parse(cfg.StorageURL)
	if err != nil || store.Host == "" || store.User != nil || store.Path != "" || store.RawQuery != "" || store.Fragment != "" || (store.Scheme != "http" && store.Scheme != "https") {
		return cfg, errors.New("SQL requires an internal object-storage URL")
	}
	for key, target := range map[string]string{"SQL_KYUUBI_URL": cfg.Kyuubi, "SQL_POLARIS_URL": cfg.Polaris} {
		u, err := url.Parse(target)
		if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
			(u.Scheme != "https" && !(u.Scheme == "http" && env("SQL_ALLOW_HTTP") == "true")) {
			return cfg, fmt.Errorf("%s must be an HTTPS endpoint (or explicitly enabled test HTTP)", key)
		}
	}
	if raw := env("SQL_BOOTSTRAP_USERS"); len(raw) > 65536 || json.Unmarshal([]byte(raw), &cfg.Bootstrap) != nil {
		return cfg, errors.New("invalid SQL bootstrap users")
	}
	for _, setting := range []struct {
		key      string
		value    *int
		min, max int
	}{
		{"SQL_MAX_ROWS", &cfg.MaxRows, 1, 100000},
		{"SQL_MAX_BYTES", &cfg.MaxBytes, 1024, 64 << 20},
		{"SQL_WORKERS", &cfg.Workers, 1, 8},
	} {
		n, err := strconv.Atoi(env(setting.key))
		if err != nil || n < setting.min || n > setting.max {
			return cfg, fmt.Errorf("invalid %s", setting.key)
		}
		*setting.value = n
	}
	cfg.Timeout, err = time.ParseDuration(env("SQL_QUERY_TIMEOUT"))
	if err != nil || cfg.Timeout < time.Minute || cfg.Timeout > 30*time.Minute {
		return cfg, errors.New("SQL_QUERY_TIMEOUT must be between 1m and 30m")
	}
	for _, setting := range []struct {
		key, fallback string
		value         *time.Duration
		min, max      time.Duration
	}{
		{"SQL_RESULT_TTL", "1h", &cfg.Retention, 30 * time.Second, 30 * 24 * time.Hour},
		{"SQL_HISTORY_RETENTION", "30d", &cfg.HistoryRetention, 24 * time.Hour, 365 * 24 * time.Hour},
		{"SQL_AUDIT_RETENTION", "90d", &cfg.AuditRetention, 24 * time.Hour, 365 * 24 * time.Hour},
	} {
		raw := env(setting.key)
		if raw == "" {
			raw = setting.fallback
		}
		d, err := retentionDuration(raw)
		if err != nil || d < setting.min || d > setting.max {
			return cfg, fmt.Errorf("%s must be between %s and %s", setting.key, setting.min, setting.max)
		}
		*setting.value = d
	}
	return cfg, nil
}

func retentionDuration(value string) (time.Duration, error) {
	if raw, ok := strings.CutSuffix(value, "d"); ok {
		days, err := strconv.ParseUint(raw, 10, 16)
		if err != nil || days > 365 {
			return 0, errors.New("invalid number of days")
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	return time.ParseDuration(value)
}

func validTable(name string) bool {
	parts := strings.Split(name, ".")
	if len(parts) < 3 || len(parts) > 8 || parts[0] != "polaris" {
		return false
	}
	if len(parts) > 3 && icebergMetadataTables[parts[len(parts)-1]] {
		return false
	}
	for _, part := range parts[1:] {
		if !catalogName.MatchString(part) {
			return false
		}
	}
	return true
}

func metadataBase(name string) (string, bool) {
	at := strings.LastIndexByte(name, '.')
	if at < 1 || !icebergMetadataTables[name[at+1:]] || !validTable(name[:at]) {
		return "", false
	}
	return name[:at], true
}
