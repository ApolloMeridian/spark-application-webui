package sqlquery

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"spark-control-center/object-storage/internal/storage"
)

const maxResponse = 8 << 20

type clients struct {
	http *http.Client
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
	return &clients{http: client, cfg: cfg}, nil
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

type objectAccess struct {
	Scope, Action, Resource string
	ViewVersion             int `json:"viewVersion,omitempty"`
}

type statementPlan struct {
	Kind   string
	Value  string
	Access []objectAccess
}

type parseFailure struct {
	status  int
	message string
}

func (err parseFailure) Error() string { return err.message }

func (c *clients) parse(ctx context.Context, sql string, scope planningContext) (statementPlan, error) {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	temps := make([]string, 0, len(scope.tempViews))
	for name := range scope.tempViews {
		temps = append(temps, name)
	}
	raw, status, err := c.request(ctx, http.MethodPost, "http://127.0.0.1:18083/parse", "text/plain", strings.NewReader(sql), func(r *http.Request) {
		r.Header.Set("X-Sql-Namespace", scope.namespace)
		r.Header.Set("X-Sql-Temp-Views", strings.Join(temps, ","))
	})
	if err != nil {
		return statementPlan{}, parseFailure{503, "Spark SQL parser unavailable"}
	}
	switch status {
	case 400:
		return statementPlan{}, parseFailure{400, "Invalid Spark SQL syntax"}
	case 422:
		return statementPlan{}, parseFailure{422, "SQL capability unavailable: " + string(raw)}
	case 200:
	default:
		return statementPlan{}, parseFailure{503, "Spark SQL parser unavailable"}
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	kind, value, _ := strings.Cut(lines[0], "\t")
	out := statementPlan{Kind: kind, Value: value, Access: []objectAccess{}}
	if (kind != "query" && kind != "metadata" && kind != "command" && kind != "use" && kind != "temp") ||
		(kind == "use" && value != "polaris" && !validNamespace(value)) || (kind == "temp" && !catalogName.MatchString(value)) || len(lines) > 65 {
		return statementPlan{}, errors.New("invalid parser response")
	}
	for _, line := range lines[1:] {
		fields := strings.Split(line, "\t")
		if len(fields) != 3 || (!validAccess(fields[0], fields[1], fields[2]) &&
			!(fields[0] == "temp" && fields[1] == "read" && catalogName.MatchString(fields[2]))) {
			return statementPlan{}, errors.New("invalid parser response")
		}
		out.Access = append(out.Access, objectAccess{Scope: fields[0], Action: fields[1], Resource: fields[2]})
	}
	return out, nil
}

type catalogSession struct {
	client *clients
	token  string
	prefix string
}

type catalogFailure int

func (e catalogFailure) Error() string { return fmt.Sprintf("Polaris operation failed (%d)", e) }

func isCatalogNotFound(err error) bool {
	var status catalogFailure
	return errors.As(err, &status) && status == http.StatusNotFound
}

func (c *clients) catalog(ctx context.Context) (catalogSession, error) {
	fail := errors.New("Polaris principal or catalog is unavailable")
	resp, status, err := c.request(ctx, http.MethodPost, c.cfg.Polaris+"/v1/oauth/tokens", "application/x-www-form-urlencoded",
		strings.NewReader(url.Values{"grant_type": {"client_credentials"}, "client_id": {c.cfg.PolarisID}, "client_secret": {c.cfg.PolarisSecret}, "scope": {"PRINCIPAL_ROLE:" + c.cfg.PolarisRole}}.Encode()),
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
	return s.call(ctx, http.MethodGet, endpoint, nil, out)
}

func (s catalogSession) call(ctx context.Context, method, endpoint string, input, out any) error {
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	raw, status, err := s.client.request(ctx, method, s.client.cfg.Polaris+endpoint, "application/json", body, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+s.token)
		r.Header.Set("Polaris-Realm", s.client.cfg.Realm)
	})
	if err != nil || status/100 != 2 || (out != nil && json.Unmarshal(raw, out) != nil) {
		return catalogFailure(status)
	}
	return nil
}

type TableInfo struct {
	UUID              string `json:"table-uuid"`
	CurrentSchemaID   int    `json:"current-schema-id"`
	CurrentSnapshotID *int64 `json:"current-snapshot-id"`
	DefaultSpecID     int    `json:"default-spec-id"`
	Comment           string `json:"comment,omitempty"`
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
	var out struct {
		Metadata struct {
			TableInfo
			Properties map[string]string `json:"properties"`
		} `json:"metadata"`
	}
	err := s.get(ctx, s.tablePath(name), &out)
	if err == nil && !uuidPattern.MatchString(out.Metadata.UUID) {
		err = errors.New("table identity missing")
	}
	out.Metadata.Comment = out.Metadata.Properties["comment"]
	return out.Metadata.TableInfo, err
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
		func(r *http.Request) { r.SetBasicAuth(p.KyuubiUser, c.engineToken(p.KyuubiUser, time.Now())) })
	if err != nil || status/100 != 2 {
		return status, fmt.Errorf("Kyuubi request failed (%d)", status)
	}
	if output != nil && json.Unmarshal(raw, output) != nil {
		return status, errors.New("invalid Kyuubi response")
	}
	return status, nil
}

func (c *clients) checkKyuubiAuth(ctx context.Context, p Profile) error {
	_, status, _ := c.request(ctx, http.MethodGet, c.cfg.Kyuubi+"/api/v1/sessions/count", "", nil,
		func(r *http.Request) { r.SetBasicAuth(p.KyuubiUser, "invalid") })
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return nil
	case status/100 == 2:
		return fmt.Errorf("Kyuubi REST accepted invalid credentials (HTTP %d); configure CUSTOM authentication", status)
	case status != 0:
		return fmt.Errorf("Kyuubi REST authentication check returned HTTP %d; verify the REST service URL", status)
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return errors.New("Kyuubi REST authentication check timed out")
	default:
		return errors.New("Could not reach Kyuubi REST; verify the service URL and connectivity")
	}
}

func (c *clients) engineToken(username string, now time.Time) string {
	expiry := fmt.Sprint(now.Add(45 * time.Second).Unix())
	mac := hmac.New(sha256.New, []byte(c.cfg.SigningKey))
	fmt.Fprintf(mac, "spark-console-kyuubi-v1\n%s\n%s", username, expiry)
	return expiry + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

type resultPage struct {
	Rows [][]json.RawMessage `json:"rows"`
}

func (c *clients) result(ctx context.Context, method, id string, page int, body io.Reader) ([]byte, error) {
	target := fmt.Sprintf("/internal/sql-results/%s/%06d.json", id, page)
	raw, _, err := c.resultRequest(ctx, method, target, body)
	return raw, err
}

func (c *clients) deleteResult(ctx context.Context, id string) (bool, error) {
	_, status, err := c.resultRequest(ctx, http.MethodDelete, "/internal/sql-results/"+id, nil)
	if err == nil && status != http.StatusAccepted && status != http.StatusNoContent {
		err = errors.New("unexpected result cleanup response")
	}
	return status == http.StatusNoContent, err
}

func (c *clients) resultRequest(ctx context.Context, method, target string, body io.Reader) ([]byte, int, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	raw, status, err := c.request(ctx, method, c.cfg.StorageURL+target, "application/json", body, func(r *http.Request) {
		r.Header.Set(storage.ResultHeader, storage.Sign([]byte(c.cfg.SigningKey), storage.Claims{
			ID: "sql-gateway", Tenant: "internal", Audience: storage.ResultAudience,
			Method: method, Target: target, Expires: time.Now().Add(45 * time.Second).Unix(),
		}))
	})
	if err != nil || status/100 != 2 || len(raw) > storage.MaxResultPage {
		return nil, status, errors.New("private result storage unavailable")
	}
	return raw, status, nil
}

func (c *clients) put(ctx context.Context, id string, page int, raw []byte) error {
	_, err := c.result(ctx, http.MethodPut, id, page, bytes.NewReader(raw))
	return err
}

func (c *clients) read(ctx context.Context, id string, page int) (resultPage, error) {
	var out resultPage
	data, err := c.result(ctx, http.MethodGet, id, page, nil)
	if err != nil || json.Unmarshal(data, &out) != nil {
		return resultPage{}, errors.New("invalid query result")
	}
	return out, nil
}
