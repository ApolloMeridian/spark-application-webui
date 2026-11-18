package sqlquery

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Profile struct {
	Tenant        string `json:"tenant"`
	KyuubiUser    string `json:"kyuubiUser"`
	KyuubiSecret  string `json:"kyuubiPassword"`
	PolarisID     string `json:"polarisClientId"`
	PolarisSecret string `json:"polarisClientSecret"`
	PolarisRole   string `json:"polarisPrincipalRole"`
}

type Config struct {
	Database, Kyuubi, Polaris, Realm, Warehouse, CAFile        string
	SigningKey, Endpoint, Region, Bucket, AccessKey, SecretKey string
	PathStyle                                                  bool
	Profiles                                                   map[string]Profile
	MaxRows, MaxBytes, Workers                                 int
	Timeout, Retention                                         time.Duration
}

var safeName = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,127}$`)
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
		Database: env("SQL_DATABASE_URL"), SigningKey: env("SQL_SIGNING_KEY"),
		Kyuubi: strings.TrimRight(env("SQL_KYUUBI_URL"), "/"), Polaris: strings.TrimRight(env("SQL_POLARIS_URL"), "/"),
		Realm: env("SQL_POLARIS_REALM"), Warehouse: env("SQL_POLARIS_WAREHOUSE"), CAFile: env("SQL_CA_FILE"),
		Endpoint: env("SQL_RESULT_ENDPOINT"), Region: env("SQL_RESULT_REGION"), Bucket: env("SQL_RESULT_BUCKET"),
		AccessKey: env("SQL_RESULT_ACCESS_KEY"), SecretKey: env("SQL_RESULT_SECRET_KEY"), PathStyle: env("SQL_RESULT_PATH_STYLE") == "true",
	}
	if len(cfg.SigningKey) < 32 || cfg.Database == "" || !userName.MatchString(cfg.Warehouse) ||
		cfg.Bucket == "" || cfg.Region == "" || cfg.AccessKey == "" || cfg.SecretKey == "" {
		return cfg, errors.New("SQL database, signing key, warehouse and private result bucket settings are required")
	}
	for key, target := range map[string]string{"SQL_KYUUBI_URL": cfg.Kyuubi, "SQL_POLARIS_URL": cfg.Polaris, "SQL_RESULT_ENDPOINT": cfg.Endpoint} {
		u, err := url.Parse(target)
		if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
			(u.Scheme != "https" && !(u.Scheme == "http" && env("SQL_ALLOW_HTTP") == "true")) {
			return cfg, fmt.Errorf("%s must be an HTTPS endpoint (or explicitly enabled test HTTP)", key)
		}
	}
	raw, err := os.ReadFile(env("SQL_PROFILES_FILE"))
	if err != nil || len(raw) > 1<<20 || json.Unmarshal(raw, &cfg.Profiles) != nil || len(cfg.Profiles) == 0 {
		return cfg, errors.New("SQL identity profiles Secret is missing or invalid")
	}
	users, principals := map[string]bool{}, map[string]bool{}
	for id, p := range cfg.Profiles {
		if id == "" || p.Tenant == "" || !userName.MatchString(p.KyuubiUser) || p.KyuubiSecret == "" ||
			strings.EqualFold(p.KyuubiUser, "anonymous") || p.PolarisID == "" || p.PolarisSecret == "" ||
			strings.Contains(strings.ToLower(p.PolarisID), "root") ||
			!userName.MatchString(p.PolarisRole) || strings.EqualFold(p.PolarisRole, "all") ||
			users[strings.ToLower(p.KyuubiUser)] || principals[p.PolarisID] {
			return cfg, errors.New("each SQL profile requires a distinct authenticated user and scoped Polaris principal")
		}
		users[strings.ToLower(p.KyuubiUser)], principals[p.PolarisID] = true, true
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
	cfg.Retention, err = time.ParseDuration(env("SQL_RESULT_TTL"))
	if err != nil || cfg.Retention < time.Minute || cfg.Retention > 24*time.Hour {
		return cfg, errors.New("SQL_RESULT_TTL must be between 1m and 24h")
	}
	return cfg, nil
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
		if !safeName.MatchString(part) {
			return false
		}
	}
	return true
}
