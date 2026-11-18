package sqlquery

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

const maxResponse = 8 << 20

type clients struct {
	http *http.Client
	s3   *s3.Client
	cfg  Config
}

func newClients(cfg Config) (*clients, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.MaxIdleConnsPerHost = 12
	if cfg.CAFile != "" {
		pem, err := os.ReadFile(cfg.CAFile)
		roots, rootErr := x509.SystemCertPool()
		if err != nil || rootErr != nil || !roots.AppendCertsFromPEM(pem) {
			return nil, errors.New("invalid SQL CA file")
		}
		transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}
	}
	client := &http.Client{Transport: transport, Timeout: 195 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	store := s3.NewFromConfig(aws.Config{
		Region: cfg.Region, Credentials: credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, ""),
		HTTPClient: client, RetryMaxAttempts: 2,
	}, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(cfg.Endpoint)
		o.UsePathStyle = cfg.PathStyle
		o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
		o.ResponseChecksumValidation = aws.ResponseChecksumValidationWhenRequired
	})
	return &clients{http: client, s3: store, cfg: cfg}, nil
}

func (c *clients) request(ctx context.Context, method, endpoint, contentType string, body io.Reader, authenticate func(*http.Request)) ([]byte, int, error) {
	r, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, 0, err
	}
	if authenticate != nil {
		authenticate(r)
	}
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	r.Header.Set("Accept", "application/json")
	response, err := c.http.Do(r)
	if err != nil {
		return nil, 0, errors.New("upstream request failed")
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxResponse+1))
	if err != nil || len(raw) > maxResponse {
		return nil, response.StatusCode, errors.New("upstream response exceeds byte budget")
	}
	return raw, response.StatusCode, nil
}

func (c *clients) parse(ctx context.Context, sql string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	raw, status, err := c.request(ctx, http.MethodPost, "http://127.0.0.1:18083/parse", "text/plain", strings.NewReader(sql), nil)
	if err != nil {
		return nil, errors.New("Spark SQL parser unavailable")
	}
	if status != 200 {
		return nil, errors.New("only supported read-only Spark SQL is accepted; use polaris.namespace.table and supported built-in functions")
	}
	tables := []string{}
	if len(raw) > 0 {
		for _, name := range strings.Split(string(raw), "\n") {
			if !validTable(name) {
				return nil, errors.New("invalid parser response")
			}
			tables = append(tables, name)
		}
	}
	return tables, nil
}

type catalogSession struct {
	client *clients
	token  string
	prefix string
}

func (c *clients) catalog(ctx context.Context, p Profile) (catalogSession, error) {
	fail := errors.New("Polaris principal or catalog is unavailable")
	resp, status, err := c.request(ctx, http.MethodPost, c.cfg.Polaris+"/v1/oauth/tokens", "application/x-www-form-urlencoded",
		strings.NewReader(url.Values{"grant_type": {"client_credentials"}, "client_id": {p.PolarisID}, "client_secret": {p.PolarisSecret}, "scope": {"PRINCIPAL_ROLE:" + p.PolarisRole}}.Encode()),
		func(r *http.Request) { r.Header.Set("Polaris-Realm", c.cfg.Realm) })
	var token struct {
		AccessToken string `json:"access_token"`
	}
	if err != nil || status != 200 || json.Unmarshal(resp, &token) != nil || token.AccessToken == "" {
		return catalogSession{}, fail
	}
	s := catalogSession{client: c, token: token.AccessToken}
	var config struct {
		Overrides map[string]string `json:"overrides"`
		Defaults  map[string]string `json:"defaults"`
	}
	if err := s.get(ctx, "/v1/config?"+url.Values{"warehouse": {c.cfg.Warehouse}}.Encode(), &config); err != nil {
		return catalogSession{}, fail
	}
	s.prefix = config.Overrides["prefix"]
	if s.prefix == "" {
		s.prefix = config.Defaults["prefix"]
	}
	if s.prefix == "" {
		s.prefix = c.cfg.Warehouse
	}
	if !userName.MatchString(s.prefix) {
		return catalogSession{}, fail
	}
	return s, nil
}

func (s catalogSession) get(ctx context.Context, endpoint string, out any) error {
	raw, status, err := s.client.request(ctx, http.MethodGet, s.client.cfg.Polaris+endpoint, "", nil, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+s.token)
		r.Header.Set("Polaris-Realm", s.client.cfg.Realm)
	})
	if err != nil || status != 200 || json.Unmarshal(raw, out) != nil {
		return errors.New("Polaris denied access or the table is unavailable")
	}
	return nil
}

type TableInfo struct {
	UUID              string `json:"table-uuid"`
	CurrentSchemaID   int    `json:"current-schema-id"`
	CurrentSnapshotID *int64 `json:"current-snapshot-id"`
	DefaultSpecID     int    `json:"default-spec-id"`
	Schemas           []struct {
		ID     int `json:"schema-id"`
		Fields []struct {
			Name     string          `json:"name"`
			Type     json.RawMessage `json:"type"`
			Required bool            `json:"required"`
		} `json:"fields"`
	} `json:"schemas"`
	PartitionSpecs []struct {
		ID     int `json:"spec-id"`
		Fields []struct {
			Name      string `json:"name"`
			Transform string `json:"transform"`
		} `json:"fields"`
	} `json:"partition-specs"`
}

func (s catalogSession) table(ctx context.Context, name string) (TableInfo, error) {
	if !validTable(name) {
		return TableInfo{}, errors.New("invalid catalog identifier")
	}
	parts := strings.Split(name, ".")
	var out struct {
		Metadata TableInfo `json:"metadata"`
	}
	namespace := url.PathEscape(strings.Join(parts[1:len(parts)-1], "\x1f"))
	err := s.get(ctx, "/v1/"+s.prefix+"/namespaces/"+namespace+"/tables/"+parts[len(parts)-1], &out)
	if err == nil && !uuidPattern.MatchString(out.Metadata.UUID) {
		err = errors.New("table identity missing")
	}
	return out.Metadata, err
}

func (c *clients) engine(ctx context.Context, p Profile, method, endpoint string, input, output any) (int, error) {
	var body io.Reader
	if input != nil {
		raw, err := json.Marshal(input)
		if err != nil {
			return 0, err
		}
		body = bytes.NewReader(raw)
	}
	raw, status, err := c.request(ctx, method, c.cfg.Kyuubi+"/api/v1"+endpoint, "application/json", body,
		func(r *http.Request) { r.SetBasicAuth(p.KyuubiUser, p.KyuubiSecret) })
	if err != nil || status/100 != 2 {
		return status, fmt.Errorf("Kyuubi request failed (%d)", status)
	}
	if output != nil && json.Unmarshal(raw, output) != nil {
		return status, errors.New("invalid Kyuubi response")
	}
	return status, nil
}

func (c *clients) checkKyuubiAuth(ctx context.Context, p Profile) error {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	p.KyuubiSecret = hex.EncodeToString(nonce[:])
	status, _ := c.engine(ctx, p, http.MethodGet, "/sessions/count", nil, nil)
	if status != http.StatusUnauthorized && status != http.StatusForbidden {
		return errors.New("Kyuubi must reject invalid credentials")
	}
	return nil
}

type resultPage struct {
	Rows [][]json.RawMessage `json:"rows"`
}

func objectKey(id string, page int) string {
	return fmt.Sprintf("spark-console-sql/%s/%06d.json", id, page)
}

func (c *clients) put(ctx context.Context, id string, page int, raw []byte) error {
	_, err := c.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(c.cfg.Bucket), Key: aws.String(objectKey(id, page)),
		Body: bytes.NewReader(raw), ContentType: aws.String("application/json"),
	})
	return err
}

func (c *clients) read(ctx context.Context, id string, page int) (resultPage, error) {
	result, err := c.s3.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(c.cfg.Bucket), Key: aws.String(objectKey(id, page)),
	})
	if err != nil {
		return resultPage{}, err
	}
	defer result.Body.Close()
	var out resultPage
	data, err := io.ReadAll(io.LimitReader(result.Body, maxResponse+1))
	if err != nil || len(data) > maxResponse || json.Unmarshal(data, &out) != nil {
		return resultPage{}, errors.New("invalid query result")
	}
	return out, nil
}
