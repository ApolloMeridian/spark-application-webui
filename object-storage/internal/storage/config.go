package storage

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"time"
)

type Connection struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Provider string   `json:"provider"`
	Buckets  []string `json:"buckets"`
	S3       struct {
		Endpoint          string `json:"endpoint"`
		Region            string `json:"region"`
		ForcePathStyle    bool   `json:"forcePathStyle"`
		AccessKeyFile     string `json:"accessKeyFile"`
		SecretKeyFile     string `json:"secretKeyFile"`
		CAFile            string `json:"caFile"`
		ConditionalWrites bool   `json:"conditionalWrites"`
	} `json:"s3"`
	GCS struct {
		ProjectID      string `json:"projectID"`
		AuthMode       string `json:"authMode"`
		CredentialFile string `json:"credentialFile"`
	} `json:"gcs"`
}
type Bootstrap struct {
	AllBuckets bool     `json:"allBuckets,omitempty"`
	Username   string   `json:"username"`
	Tenant     string   `json:"tenantId"`
	Connection string   `json:"connection"`
	Bucket     string   `json:"bucket"`
	Prefix     string   `json:"prefix"`
	Actions    []string `json:"actions"`
}

// Expand once per scan so newly configured buckets receive an explicit one-time
// bootstrap grant, without broadening any ordinary user grant or restoring revocations.
func (c Config) ExpandedBootstrap() ([]Bootstrap, error) {
	out := []Bootstrap{}
	for _, grant := range c.Bootstrap {
		found := false
		for _, connection := range c.Connections {
			if connection.ID != grant.Connection {
				continue
			}
			for _, bucket := range connection.Buckets {
				if !grant.AllBuckets && bucket != grant.Bucket {
					continue
				}
				x := grant
				x.Bucket = bucket
				x.AllBuckets = false
				out = append(out, x)
				found = true
			}
		}
		if !found {
			return nil, fmt.Errorf("bootstrap grant refers to unconfigured bucket")
		}
	}
	return out, nil
}

type Config struct {
	Connections            []Connection `json:"connections"`
	Bootstrap              []Bootstrap  `json:"bootstrapGrants"`
	MaxUpload              int64        `json:"maxUploadBytes"`
	MaxDownload            int64        `json:"maxDownloadBytes"`
	PartSize               int64        `json:"partSizeBytes"`
	TextMaxBytes           int64        `json:"textMaxBytes"`
	Concurrent             int          `json:"maxConcurrentTransfers"`
	RetentionDays          int          `json:"retentionDays"`
	RequestsPerMinute      int          `json:"requestsPerMinute"`
	TransferTimeoutSeconds int          `json:"transferTimeoutSeconds"`
}

func LoadConfig(path string) (Config, error) {
	var c Config
	raw, e := os.ReadFile(path)
	if e != nil {
		return c, e
	}
	if e = json.Unmarshal(raw, &c); e != nil {
		return c, e
	}
	return c, c.Validate()
}
func (c *Config) Validate() error {
	if c.TextMaxBytes == 0 {
		c.TextMaxBytes = TextLimit
	}
	if c.TextMaxBytes < 1024 || c.TextMaxBytes > 8<<20 {
		return fmt.Errorf("textMaxBytes must be between 1024 and 8388608")
	}
	if c.MaxUpload == 0 {
		c.MaxUpload = 1 << 30
	}
	if c.MaxDownload == 0 {
		c.MaxDownload = 1 << 30
	}
	if c.PartSize == 0 {
		c.PartSize = 8 << 20
	}
	if c.Concurrent == 0 {
		c.Concurrent = 4
	}
	if c.RetentionDays == 0 {
		c.RetentionDays = 30
	}
	if c.RequestsPerMinute == 0 {
		c.RequestsPerMinute = 600
	}
	if c.TransferTimeoutSeconds == 0 {
		c.TransferTimeoutSeconds = 600
	}
	if c.MaxUpload < 1 || c.MaxUpload > 1<<40 || c.MaxDownload < 1 || c.MaxDownload > 1<<40 || c.PartSize < 5<<20 || c.PartSize > 16<<20 || c.PartSize%(256<<10) != 0 || c.Concurrent < 1 || c.Concurrent > 8 || c.RetentionDays < 1 || c.RetentionDays > 3650 || c.RequestsPerMinute < 1 || c.RequestsPerMinute > 6000 || c.TransferTimeoutSeconds < 30 || c.TransferTimeoutSeconds > 3600 || len(c.Connections) == 0 || len(c.Connections) > 20 {
		return fmt.Errorf("invalid storage limits or connections")
	}
	seen := map[string]bool{}
	validID := regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)
	for _, x := range c.Connections {
		if !validID.MatchString(x.ID) || seen[x.ID] || len(x.Buckets) == 0 || x.Name == "" {
			return fmt.Errorf("invalid connection identity")
		}
		seen[x.ID] = true
		for _, b := range x.Buckets {
			if b == "" || len(b) > 63 || !regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`).MatchString(b) {
				return fmt.Errorf("invalid bucket")
			}
		}
		switch x.Provider {
		case "s3":
			u, e := url.Parse(x.S3.Endpoint)
			if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || (u.Scheme != "https" && u.Scheme != "http") || x.S3.Region == "" || x.S3.AccessKeyFile == "" || x.S3.SecretKeyFile == "" {
				return fmt.Errorf("invalid S3 configuration")
			}
		case "gcs":
			if x.GCS.AuthMode != "gkeWif" && x.GCS.AuthMode != "kubernetesWif" {
				return fmt.Errorf("GCS requires WIF")
			}
			if x.GCS.AuthMode == "kubernetesWif" && x.GCS.CredentialFile == "" {
				return fmt.Errorf("WIF credential configuration required")
			}
		default:
			return fmt.Errorf("unsupported storage provider")
		}
	}
	return nil
}
func (c Config) Timeout() time.Duration { return time.Duration(c.TransferTimeoutSeconds) * time.Second }
