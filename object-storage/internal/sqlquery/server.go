package sqlquery

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var uuidPattern = regexp.MustCompile(`^[a-f0-9]{8}(-[a-f0-9]{4}){3}-[a-f0-9]{12}$`)

type Server struct {
	db      *pgxpool.Pool
	clients *clients
	cfg     Config
	workers chan struct{}
	wake    chan struct{}
}

type actor struct {
	ID       string `json:"id"`
	Tenant   string `json:"tenant"`
	Method   string `json:"method"`
	Target   string `json:"target"`
	Audience string `json:"aud"`
	Expires  int64  `json:"exp"`
}

type user struct {
	actor
	Name           string
	Manager        bool
	CatalogManager bool
}
type grant struct {
	User    string   `json:"userId"`
	Tenant  string   `json:"tenantId,omitempty"`
	Table   string   `json:"table"`
	UUID    string   `json:"tableUuid"`
	Scope   string   `json:"scope"`
	Actions []string `json:"actions"`
	version string
}
type column struct {
	Name string `json:"columnName"`
	Type string `json:"dataType"`
}
type query struct {
	ID              string         `json:"id"`
	State           string         `json:"state"`
	Tables          []string       `json:"tables"`
	Columns         []column       `json:"columns"`
	Rows            int            `json:"rowCount"`
	Pages           int            `json:"pages"`
	Truncated       bool           `json:"truncated"`
	Message         string         `json:"message,omitempty"`
	Operation       string         `json:"operationId,omitempty"`
	Created         time.Time      `json:"createdAt"`
	Expires         *time.Time     `json:"expiresAt,omitempty"`
	ResultState     string         `json:"resultState"`
	Catalog         string         `json:"-"`
	Write           string         `json:"writeTable,omitempty"`
	Plan            *statementPlan `json:"-"`
	Ordinal         int            `json:"ordinal"`
	Steps           []query        `json:"steps,omitempty"`
	RefreshBranches []string       `json:"refreshBranches,omitempty"`
}

const schema = `
CREATE TABLE IF NOT EXISTS console_sql_grants(
 tenant text NOT NULL, user_id text NOT NULL, table_name text NOT NULL, table_uuid text NOT NULL,
 grant_id uuid NOT NULL DEFAULT gen_random_uuid(),
 PRIMARY KEY(tenant,user_id,table_name)
);
ALTER TABLE console_sql_grants ADD COLUMN IF NOT EXISTS grant_id uuid NOT NULL DEFAULT gen_random_uuid();
CREATE TABLE IF NOT EXISTS console_sql_queries(
 id uuid PRIMARY KEY, tenant text NOT NULL, user_id text NOT NULL, actor_name text NOT NULL,
 statement text NOT NULL, fingerprint text NOT NULL, request_key uuid NOT NULL,
 tables text[] NOT NULL, table_uuids text[] NOT NULL, state text NOT NULL DEFAULT 'queued',
 columns jsonb NOT NULL DEFAULT '[]', rows integer NOT NULL DEFAULT 0, pages integer NOT NULL DEFAULT 0,
 truncated boolean NOT NULL DEFAULT false, message text NOT NULL DEFAULT '', operation text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT now(), expires_at timestamptz, worker_deadline timestamptz,
 cancel_requested boolean NOT NULL DEFAULT false,
 UNIQUE(tenant,user_id,request_key)
);
CREATE INDEX IF NOT EXISTS console_sql_owner_idx ON console_sql_queries(tenant,user_id,created_at DESC);
CREATE INDEX IF NOT EXISTS console_sql_queue_idx ON console_sql_queries(created_at) WHERE state='queued';
`

func Open(ctx context.Context, cfg Config) (*Server, error) {
	client, err := newClients(cfg)
	if err != nil {
		return nil, err
	}
	conf, err := pgxpool.ParseConfig(cfg.Database)
	if err != nil {
		return nil, errors.New("invalid SQL database URL")
	}
	conf.MaxConns = 16
	db, err := pgxpool.NewWithConfig(ctx, conf)
	if err != nil {
		return nil, errors.New("SQL database unavailable")
	}
	tx, err := db.Begin(ctx)
	if err == nil {
		defer tx.Rollback(ctx)
		_, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(16001601)")
	}
	if err == nil {
		_, err = tx.Exec(ctx, schema)
	}
	if err == nil {
		_, err = tx.Exec(ctx, permissionSchema)
	}
	if err == nil {
		_, err = tx.Exec(ctx, lifecycleSchema)
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		db.Close()
		return nil, errors.New("SQL database initialization failed")
	}
	s := &Server{db: db, clients: client, cfg: cfg, workers: make(chan struct{}, cfg.Workers), wake: make(chan struct{}, 1)}
	go s.dispatch(ctx)
	go s.maintainHistory(ctx)
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for ctx.Err() == nil {
			step, cancel := context.WithTimeout(ctx, 5*time.Second)
			dbError(s.bootstrap(step))
			cancel()
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return s, nil
}

func (s *Server) Close() { s.db.Close() }

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		var ready bool
		err := s.db.QueryRow(ctx, "SELECT to_regclass('local_users') IS NOT NULL AND to_regclass('operation_audit') IS NOT NULL").Scan(&ready)
		if err != nil || !ready {
			reply(w, 503, nil, "console database is not ready")
			return
		}
		w.WriteHeader(204)
	})
	mux.HandleFunc("GET /v1/tables", s.auth(s.tables))
	mux.HandleFunc("GET /v1/namespaces", s.auth(s.namespaces))
	mux.HandleFunc("GET /v1/table", s.auth(s.table))
	mux.HandleFunc("GET /v1/views", s.auth(s.views))
	mux.HandleFunc("GET /v1/view", s.auth(s.view))
	mux.HandleFunc("POST /v1/catalog", s.auth(s.mutateCatalog))
	mux.HandleFunc("GET /v1/grants", s.auth(s.grants))
	mux.HandleFunc("PUT /v1/grants", s.auth(s.grant))
	mux.HandleFunc("DELETE /v1/grants", s.auth(s.revoke))
	mux.HandleFunc("POST /v1/queries", s.auth(s.submit))
	mux.HandleFunc("GET /v1/queries", s.auth(s.history))
	mux.HandleFunc("GET /v1/queries/{id}", s.auth(s.status))
	mux.HandleFunc("GET /v1/queries/{id}/statement", s.auth(s.statementText))
	mux.HandleFunc("POST /v1/queries/{id}/cancel", s.auth(s.cancel))
	mux.HandleFunc("GET /v1/queries/{id}/pages/{page}", s.auth(s.page))
	mux.HandleFunc("GET /v1/queries/{id}/download", s.auth(s.download))
	return mux
}

func verify(r *http.Request, key string, now time.Time) (actor, bool) {
	var claim actor
	encoded := r.Header.Get("X-Spark-SQL-Identity")
	payload, signature, ok := strings.Cut(encoded, ".")
	if !ok || len(encoded) > 4096 {
		return claim, false
	}
	sig, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil {
		return claim, false
	}
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(payload))
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return claim, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil || json.Unmarshal(raw, &claim) != nil {
		return claim, false
	}
	return claim, claim.Audience == "spark-console-sql" && claim.ID != "" && claim.Tenant != "" &&
		claim.Method == r.Method && claim.Target == r.URL.RequestURI() && claim.Expires > now.Unix() &&
		claim.Expires <= now.Add(time.Minute).Unix()
}

func (s *Server) actualUser(ctx context.Context, claim actor) (user, error) {
	x := user{actor: claim}
	err := s.db.QueryRow(ctx, `SELECT u.username,COALESCE(u.role='admin' AND t.permissions->'sqlManage'='true'::jsonb,false),
		EXISTS (SELECT 1 FROM console_sql_permissions p WHERE p.tenant=u.tenant_id AND p.user_id=u.id AND p.catalog=$3
			AND p.scope='catalog' AND p.actions @> $4)
		FROM local_users u JOIN tenants t ON t.id=u.tenant_id
		WHERE u.id=$1 AND u.tenant_id=$2 AND NOT u.disabled AND t.permissions->'sqlQuery'='true'::jsonb`,
		claim.ID, claim.Tenant, s.cfg.catalogID(), catalogActions).Scan(&x.Name, &x.Manager, &x.CatalogManager)
	return x, err
}

func (s *Server) auth(next func(http.ResponseWriter, *http.Request, user)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		claim, ok := verify(r, s.cfg.SigningKey, time.Now())
		if !ok {
			reply(w, 401, nil, "invalid SQL gateway identity")
			return
		}
		x, err := s.actualUser(r.Context(), claim)
		if err != nil {
			reply(w, 403, nil, "SQL access is unavailable for this user")
			return
		}
		next(w, r, x)
	}
}

func reply(w http.ResponseWriter, status int, value any, message string) {
	if message != "" {
		value = map[string]string{"message": message}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func decode(w http.ResponseWriter, r *http.Request, value any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536))
	d.DisallowUnknownFields()
	var extra any
	if d.Decode(value) != nil || d.Decode(&extra) != io.EOF {
		reply(w, 400, nil, "invalid request")
		return false
	}
	return true
}

func audit(ctx context.Context, tx pgx.Tx, x user, id, action, result, detail string) error {
	return insertAudit(ctx, tx, x, id, action, result, "", detail, nil)
}

func insertAudit(ctx context.Context, tx pgx.Tx, x user, id, action, result, reason, detail string, fields []byte) error {
	detail = "userId=" + x.ID + " " + detail
	if len(detail) > 2048 {
		detail = strings.ToValidUTF8(detail[:2048], "")
	}
	_, err := tx.Exec(ctx, `INSERT INTO operation_audit(id,tenant_id,application_name,namespace,operator_name,operation,reason,occurred_at,result,message,sql_details)
		VALUES(gen_random_uuid()::text,$1,$2,'',$3,$4,$5,clock_timestamp(),$6,$7,$8::jsonb)`, x.Tenant, id, x.Name, action, reason, result, detail, fields)
	return err
}

func (s *Server) grants(w http.ResponseWriter, r *http.Request, x user) {
	if !x.Manager {
		reply(w, 403, nil, "SQL grant management permission required")
		return
	}
	rows, err := s.grantsFor(r.Context(), x, true)
	if err != nil {
		reply(w, 503, nil, "table grants unavailable")
		return
	}
	eligible, err := s.db.Query(r.Context(), `SELECT u.id,u.username,t.name FROM local_users u JOIN tenants t ON t.id=u.tenant_id
		WHERE (u.tenant_id=$1 OR $2) AND NOT u.disabled ORDER BY u.username`, x.Tenant, x.CatalogManager)
	if err != nil {
		reply(w, 503, nil, "SQL identities unavailable")
		return
	}
	defer eligible.Close()
	users := []map[string]string{}
	for eligible.Next() {
		var id, name, tenant string
		if eligible.Scan(&id, &name, &tenant) != nil {
			reply(w, 503, nil, "SQL identities unavailable")
			return
		}
		users = append(users, map[string]string{"id": id, "name": name, "tenant": tenant})
	}
	if eligible.Err() != nil {
		reply(w, 503, nil, "SQL identities unavailable")
		return
	}
	reply(w, 200, map[string]any{"grants": rows, "users": users, "catalogAdmin": x.CatalogManager}, "")
}

func (s *Server) grant(w http.ResponseWriter, r *http.Request, x user) {
	s.changeGrant(w, r, x, true)
}

func (s *Server) revoke(w http.ResponseWriter, r *http.Request, x user) {
	s.changeGrant(w, r, x, false)
}

func tableUUID(grantVersion string) string {
	id, _, _ := strings.Cut(grantVersion, ":")
	return id
}

func csvCell(value json.RawMessage) string {
	if string(value) == "null" {
		return ""
	}
	var text string
	if json.Unmarshal(value, &text) != nil {
		text = string(value)
	}
	leading := strings.TrimLeft(text, " \t\r\n")
	if leading != "" && strings.ContainsRune("=+-@", rune(leading[0])) {
		return "'" + text
	}
	if strings.HasPrefix(text, "\t") || strings.HasPrefix(text, "\r") {
		return "'" + text
	}
	return text
}

func (s *Server) download(w http.ResponseWriter, r *http.Request, x user) {
	q, ids, err := s.owned(r, x)
	if err == nil && q.ResultState == "expired" {
		reply(w, 410, nil, "result expired")
		return
	}
	if err != nil || !s.canRead(r.Context(), x, q, ids) {
		reply(w, 404, nil, "result unavailable")
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		reply(w, 503, nil, "audit unavailable")
		return
	}
	defer tx.Rollback(r.Context())
	if audit(r.Context(), tx, x, q.ID, "SQL_DOWNLOAD", "SUCCESS", "CSV stream opened") != nil || tx.Commit(r.Context()) != nil {
		reply(w, 503, nil, "audit unavailable")
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="query-`+q.ID+`.csv"`)
	writer := csv.NewWriter(w)
	headers := make([]string, len(q.Columns))
	for i, c := range q.Columns {
		raw, _ := json.Marshal(c.Name)
		headers[i] = csvCell(raw)
	}
	if writer.Write(headers) != nil {
		return
	}
	for i := 1; i <= q.Pages; i++ {
		if i > 1 && !s.canRead(r.Context(), x, q, ids) {
			panic(http.ErrAbortHandler)
		}
		page, err := s.clients.read(r.Context(), q.ID, i)
		if err != nil {
			panic(http.ErrAbortHandler)
		}
		for _, row := range page.Rows {
			cells := make([]string, len(row))
			for j, value := range row {
				cells[j] = csvCell(value)
			}
			if writer.Write(cells) != nil {
				return
			}
		}
		writer.Flush()
		if writer.Error() != nil {
			return
		}
	}
	writer.Flush()
}

func (s *Server) page(w http.ResponseWriter, r *http.Request, x user) {
	rawPage, err := strconv.Atoi(r.PathValue("page"))
	if err != nil || rawPage < 1 {
		reply(w, 400, nil, "invalid page")
		return
	}
	q, ids, err := s.owned(r, x)
	if err == nil && q.ResultState == "expired" {
		reply(w, 410, nil, "result expired")
		return
	}
	if err != nil || rawPage > q.Pages || !s.canRead(r.Context(), x, q, ids) {
		reply(w, 404, nil, "result unavailable")
		return
	}
	out, err := s.clients.read(r.Context(), q.ID, rawPage)
	if err != nil {
		reply(w, 503, nil, "result storage unavailable")
		return
	}
	if !s.canRead(r.Context(), x, q, ids) {
		reply(w, 404, nil, "result unavailable")
		return
	}
	reply(w, 200, out, "")
}

func fingerprint(sql string) string {
	s := sha256.Sum256([]byte(sql))
	return hex.EncodeToString(s[:])
}

func (s *Server) notify() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func dbError(err error) {
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		log.Print("SQL persistence operation failed")
	}
}
