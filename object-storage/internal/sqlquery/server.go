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
	Name    string
	Manager bool
}
type grant struct {
	User  string `json:"userId"`
	Table string `json:"table"`
	UUID  string `json:"tableUuid"`
}
type column struct {
	Name string `json:"columnName"`
	Type string `json:"dataType"`
}
type query struct {
	ID        string     `json:"id"`
	State     string     `json:"state"`
	Tables    []string   `json:"tables"`
	Columns   []column   `json:"columns"`
	Rows      int        `json:"rowCount"`
	Pages     int        `json:"pages"`
	Truncated bool       `json:"truncated"`
	Message   string     `json:"message,omitempty"`
	Operation string     `json:"operationId,omitempty"`
	Created   time.Time  `json:"createdAt"`
	Expires   *time.Time `json:"expiresAt,omitempty"`
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
		err = tx.Commit(ctx)
	}
	if err != nil {
		db.Close()
		return nil, errors.New("SQL database initialization failed")
	}
	s := &Server{db: db, clients: client, cfg: cfg, workers: make(chan struct{}, cfg.Workers), wake: make(chan struct{}, 1)}
	go s.dispatch(ctx)
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
	mux.HandleFunc("GET /v1/table", s.auth(s.table))
	mux.HandleFunc("GET /v1/grants", s.auth(s.grants))
	mux.HandleFunc("PUT /v1/grants", s.auth(s.grant))
	mux.HandleFunc("DELETE /v1/grants", s.auth(s.revoke))
	mux.HandleFunc("POST /v1/queries", s.auth(s.submit))
	mux.HandleFunc("GET /v1/queries", s.auth(s.history))
	mux.HandleFunc("GET /v1/queries/{id}", s.auth(s.status))
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
	err := s.db.QueryRow(ctx, `SELECT u.username,COALESCE(u.role='admin' AND t.permissions->'sqlManage'='true'::jsonb,false)
		FROM local_users u JOIN tenants t ON t.id=u.tenant_id
		WHERE u.id=$1 AND u.tenant_id=$2 AND NOT u.disabled AND t.permissions->'sqlQuery'='true'::jsonb`, claim.ID, claim.Tenant).Scan(&x.Name, &x.Manager)
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
	_, err := tx.Exec(ctx, `INSERT INTO operation_audit(id,tenant_id,application_name,namespace,operator_name,operation,reason,occurred_at,result,message)
		VALUES(gen_random_uuid()::text,$1,$2,'',$3,$4,'',now(),$5,$6)`, x.Tenant, id, x.Name, action, result, detail)
	return err
}

func (s *Server) grantsFor(ctx context.Context, x user, all bool) ([]grant, error) {
	rows, err := s.db.Query(ctx, `SELECT user_id,table_name,table_uuid FROM console_sql_grants
		WHERE tenant=$1 AND ($2 OR user_id=$3) ORDER BY table_name,user_id LIMIT 5001`, x.Tenant, all, x.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []grant{}
	for rows.Next() {
		var g grant
		if err := rows.Scan(&g.User, &g.Table, &g.UUID); err != nil {
			return nil, err
		}
		list = append(list, g)
	}
	return list, rows.Err()
}

func (s *Server) tables(w http.ResponseWriter, r *http.Request, x user) {
	list, err := s.grantsFor(r.Context(), x, false)
	if err != nil {
		reply(w, 503, nil, "table grants unavailable")
		return
	}
	reply(w, 200, list, "")
}

func (s *Server) table(w http.ResponseWriter, r *http.Request, x user) {
	name := r.URL.Query().Get("name")
	ids, err := s.authorize(r.Context(), x, []string{name})
	if err != nil {
		reply(w, 404, nil, "table unavailable")
		return
	}
	p, ok := s.cfg.Profiles[x.ID]
	if !ok || p.Tenant != x.Tenant {
		reply(w, 403, nil, "SQL identity is not provisioned")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	session, err := s.clients.catalog(ctx, p)
	if err != nil {
		reply(w, 503, nil, err.Error())
		return
	}
	info, err := session.table(ctx, name)
	if err != nil || info.UUID != tableUUID(ids[0]) {
		reply(w, 404, nil, "table unavailable or replaced")
		return
	}
	reply(w, 200, info, "")
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
	eligible, err := s.db.Query(r.Context(), `SELECT id,username FROM local_users WHERE tenant_id=$1 AND NOT disabled ORDER BY username`, x.Tenant)
	if err != nil {
		reply(w, 503, nil, "SQL identities unavailable")
		return
	}
	defer eligible.Close()
	users := []map[string]string{}
	for eligible.Next() {
		var id, name string
		if eligible.Scan(&id, &name) != nil {
			reply(w, 503, nil, "SQL identities unavailable")
			return
		}
		if p, ok := s.cfg.Profiles[id]; ok && p.Tenant == x.Tenant {
			users = append(users, map[string]string{"id": id, "name": name})
		}
	}
	if eligible.Err() != nil {
		reply(w, 503, nil, "SQL identities unavailable")
		return
	}
	reply(w, 200, map[string]any{"grants": rows, "users": users}, "")
}

func (s *Server) grant(w http.ResponseWriter, r *http.Request, x user) {
	s.changeGrant(w, r, x, true)
}

func (s *Server) revoke(w http.ResponseWriter, r *http.Request, x user) {
	s.changeGrant(w, r, x, false)
}

func (s *Server) changeGrant(w http.ResponseWriter, r *http.Request, x user, add bool) {
	if !x.Manager {
		reply(w, 403, nil, "SQL grant management permission required")
		return
	}
	var g grant
	if !decode(w, r, &g) {
		return
	}
	if !validTable(g.Table) || g.User == "" {
		reply(w, 400, nil, "invalid grant")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	if add {
		p, ok := s.cfg.Profiles[g.User]
		if !ok || p.Tenant != x.Tenant {
			reply(w, 404, nil, "SQL identity unavailable")
			return
		}
		session, err := s.clients.catalog(ctx, p)
		if err != nil {
			reply(w, 409, nil, "provision the user's Polaris role first")
			return
		}
		info, err := session.table(ctx, g.Table)
		if err != nil {
			reply(w, 409, nil, "the user's Polaris principal cannot load the table")
			return
		}
		g.UUID = info.UUID
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		reply(w, 503, nil, "SQL database unavailable")
		return
	}
	defer tx.Rollback(ctx)
	action := "SQL_REVOKE"
	if add {
		action = "SQL_GRANT"
		ct, e := tx.Exec(ctx, `INSERT INTO console_sql_grants(tenant,user_id,table_name,table_uuid)
			SELECT $1,id,$3,$4 FROM local_users WHERE tenant_id=$1 AND id=$2 AND NOT disabled
			ON CONFLICT(tenant,user_id,table_name) DO UPDATE SET table_uuid=EXCLUDED.table_uuid,
			grant_id=CASE WHEN console_sql_grants.table_uuid=EXCLUDED.table_uuid
				THEN console_sql_grants.grant_id ELSE EXCLUDED.grant_id END`,
			x.Tenant, g.User, g.Table, g.UUID)
		err = e
		if err == nil && ct.RowsAffected() == 0 {
			reply(w, 404, nil, "SQL identity unavailable")
			return
		}
	} else {
		_, err = tx.Exec(ctx, "DELETE FROM console_sql_grants WHERE tenant=$1 AND user_id=$2 AND table_name=$3", x.Tenant, g.User, g.Table)
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE console_sql_queries
				SET expires_at=LEAST(expires_at,now()),
				    cancel_requested=CASE WHEN state IN ('queued','running') THEN true ELSE cancel_requested END,
				    statement=CASE WHEN state='queued' THEN '' ELSE statement END,
				    state=CASE WHEN state='queued' THEN 'cancelled' ELSE state END
				WHERE tenant=$1 AND user_id=$2 AND $3=ANY(tables)
				    AND (state IN ('queued','running') OR (state='succeeded' AND expires_at>now()))`,
				x.Tenant, g.User, g.Table)
		}
	}
	if err == nil {
		err = audit(ctx, tx, x, g.Table, action, "SUCCESS", "user="+g.User)
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		reply(w, 503, nil, "grant update failed")
		return
	}
	reply(w, 200, g, "")
}

func (s *Server) authorize(ctx context.Context, x user, names []string) ([]string, error) {
	ids := make([]string, len(names))
	for i, name := range names {
		if !validTable(name) {
			return nil, errors.New("invalid table name")
		}
		if err := s.db.QueryRow(ctx, `SELECT table_uuid || ':' || grant_id::text FROM console_sql_grants
			WHERE tenant=$1 AND user_id=$2 AND table_name=$3`, x.Tenant, x.ID, name).Scan(&ids[i]); err != nil {
			return nil, errors.New("table access denied")
		}
	}
	return ids, nil
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
