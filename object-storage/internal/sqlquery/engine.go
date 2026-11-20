package sqlquery

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

type handle struct {
	ID string `json:"identifier"`
}
type engineRow struct {
	Fields []struct {
		Type  string          `json:"dataType"`
		Value json.RawMessage `json:"value"`
	} `json:"fields"`
}
type engineResult struct {
	Rows []engineRow `json:"rows"`
}

var sparkErrorClass = regexp.MustCompile(`\[([A-Z][A-Z0-9_]{2,70}(?:\.[A-Z0-9_]{1,70})?)\]`)

func (s *Server) run(ctx context.Context, job work) {
	ctx, stop := context.WithTimeout(ctx, s.cfg.Timeout)
	defer stop()
	go s.keepLease(ctx, job, stop)
	if job.q.statementPlan().Kind == "script" {
		finished, err := s.executeScript(ctx, job)
		if !finished {
			s.finish(job, job.q, err)
		}
		return
	}
	if statements, err := splitStatements(job.SQL); err != nil || len(statements) != 1 {
		s.finish(job, job.q, errors.New("could not recover the queued SQL statement"))
		return
	} else {
		job.SQL = statements[0]
	}
	q, err := s.execute(ctx, job)
	s.finish(job, q, err)
}

func (s *Server) execute(ctx context.Context, job work) (query, error) {
	bindings, err := s.bindQuery(ctx, job)
	if err != nil {
		return job.q, err
	}
	p, session, closeSession, err := s.openEngineSession(ctx, job)
	if err != nil {
		return job.q, err
	}
	defer closeSession()
	return s.executeStatement(ctx, job, p, session, job.q.ID, bindings)
}

func (s *Server) openEngineSession(ctx context.Context, job work) (Profile, string, func(), error) {
	p := engineProfile(job.x)
	if _, err := s.actualUser(ctx, job.x.actor); err != nil {
		return p, "", nil, errors.New("SQL access was revoked")
	}
	check, cancel := context.WithTimeout(ctx, 8*time.Second)
	err := s.clients.checkKyuubiAuth(check, p)
	cancel()
	if err != nil {
		return p, "", nil, err
	}
	var session handle
	status, err := s.clients.engine(ctx, p, http.MethodPost, "/sessions", map[string]any{"configs": map[string]string{
		"spark.sql.sparkconsole.session": job.q.ID,
		"spark.sql.iceberg.merge-schema": "false",
	}}, &session)
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return p, "", nil, fmt.Errorf("Kyuubi REST rejected the signed console identity (HTTP %d); verify the shared signing key", status)
	}
	if err != nil || !uuidPattern.MatchString(session.ID) {
		return p, "", nil, errors.New("could not open Kyuubi SQL session")
	}
	sessionPath := "/sessions/" + session.ID
	closeSession := func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		_, _ = s.clients.engine(cleanup, p, http.MethodDelete, sessionPath, nil, nil)
	}
	if err := s.verifyCatalogBinding(ctx, p, sessionPath); err != nil {
		closeSession()
		return p, "", nil, err
	}
	if err := s.verifySQLValue(ctx, p, sessionPath, "SELECT spark_console_guard_version()", "spark-console-driver-v3", 0); err != nil {
		closeSession()
		return p, "", nil, errors.New("Spark Driver Guard is unavailable")
	}
	if err := s.verifySetting(ctx, p, sessionPath, "spark.sql.sparkconsole.session", job.q.ID); err != nil {
		closeSession()
		return p, "", nil, err
	}
	return p, sessionPath, closeSession, nil
}

func (s *Server) executeStatement(ctx context.Context, job work, p Profile, sessionPath, sessionID, bindings string) (query, error) {
	q := job.q
	plan := q.statementPlan()
	if _, err := s.actualUser(ctx, job.x.actor); err != nil {
		return q, errors.New("SQL access was revoked")
	}
	grants, err := s.authorize(ctx, job.x, plan.Access)
	if err != nil || !slices.Equal(grants, job.UUIDs) {
		return q, errors.New("table permission changed")
	}
	var operation handle
	// Add a bounded top-level result limit only after the Spark parser has
	// accepted a single read-only query. A newline terminates any SQL comment.
	sql := job.SQL
	if plan.Kind == "query" {
		sql = "SELECT * FROM (\n" + sql + "\n) AS console_result LIMIT " + strconv.Itoa(s.cfg.MaxRows+1)
	}
	if err := s.installTicket(ctx, p, sessionPath, sessionID, job, sql, bindings); err != nil {
		return q, err
	}
	var active bool
	if err := s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM console_sql_queries
		WHERE id=$1 AND state='running' AND NOT cancel_requested AND worker_deadline>now())`, sessionID).Scan(&active); err != nil || !active {
		return q, errors.New("SQL run is no longer active")
	}
	_, err = s.clients.engine(ctx, p, http.MethodPost, sessionPath+"/operations/statement", map[string]any{
		"statement": sql, "runAsync": true, "queryTimeout": int(s.cfg.Timeout.Seconds()),
	}, &operation)
	if err != nil || !uuidPattern.MatchString(operation.ID) {
		return q, errors.New("Kyuubi did not accept the statement")
	}
	q.Operation = operation.ID
	op := "/operations/" + operation.ID
	finished := false
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		if !finished || ctx.Err() != nil {
			_, _ = s.clients.engine(cleanup, p, http.MethodPut, op, map[string]string{"action": "cancel"}, nil)
		}
		_, _ = s.clients.engine(cleanup, p, http.MethodPut, op, map[string]string{"action": "close"}, nil)
	}()
	tag, err := s.db.Exec(ctx, `UPDATE console_sql_queries SET operation=$2 WHERE id=$1 AND state='running'
		AND (worker_deadline>now() OR EXISTS(SELECT 1 FROM console_sql_queries p WHERE p.id=$3
			AND p.state='running' AND p.worker_deadline>now() AND NOT p.cancel_requested))`, q.ID, q.Operation, sessionID)
	if err != nil || tag.RowsAffected() != 1 {
		return q, errors.New("could not record the operation")
	}
	if err := s.waitOperation(ctx, p, op); err != nil {
		return q, err
	}
	finished = true
	if plan.Kind != "query" && plan.Kind != "metadata" {
		return q, nil
	}

	var metadata struct {
		Columns []column `json:"columns"`
	}
	if _, err := s.clients.engine(ctx, p, http.MethodGet, op+"/resultsetmetadata", nil, &metadata); err != nil ||
		len(metadata.Columns) == 0 || len(metadata.Columns) > 512 {
		return q, errors.New("result schema unavailable or too wide")
	}
	q.Columns = metadata.Columns
	q, err = s.savePages(ctx, p, op, q)
	return q, err
}

func (s *Server) bindQuery(ctx context.Context, job work) (string, error) {
	q := job.q
	plan := q.statementPlan()
	grants, err := s.authorize(ctx, job.x, plan.Access)
	if err != nil || !slices.Equal(grants, job.UUIDs) {
		return "", errors.New("table permission changed")
	}
	if len(grants) == 0 {
		return "-", nil
	}
	check, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	catalog, err := s.clients.catalog(check)
	if err != nil {
		return "", err
	}
	for _, target := range plan.Targets {
		if target.Type != "renameTo" {
			continue
		}
		if _, err := catalog.table(check, target.Resource); err == nil {
			return "", errors.New("rename destination already exists")
		} else if !isCatalogNotFound(err) {
			return "", errors.New("could not verify rename destination")
		}
		if _, err := catalog.view(check, target.Resource); err == nil {
			return "", errors.New("rename destination already exists")
		} else if !isCatalogNotFound(err) {
			return "", errors.New("could not verify rename destination")
		}
	}
	bindings := []string{}
	tableUUIDs := make(map[string]string)
	for i, target := range plan.Access {
		if target.Scope != "table" && target.Scope != "view" {
			continue
		}
		name := target.Resource
		var uuid string
		var version string
		if target.Scope == "view" {
			info, err := catalog.view(check, name)
			if err != nil || (target.ViewVersion != 0 && target.ViewVersion != info.CurrentVersion) {
				return "", errors.New("view unavailable or has been replaced")
			}
			uuid = info.UUID
			version = ":" + strconv.Itoa(info.CurrentVersion)
		} else {
			uuid = tableUUIDs[name]
			if uuid == "" {
				info, err := catalog.table(check, name)
				if err != nil {
					if isCatalogNotFound(err) && slices.Contains([]string{"write", "overwrite", "update", "merge", "deleteRows"}, target.Action) {
						return "", errors.New("write target must be an existing managed Iceberg table; views are read-only")
					}
					return "", errors.New("table unavailable or has been replaced")
				}
				uuid = info.UUID
				tableUUIDs[name] = uuid
			}
		}
		expected := tableUUID(grants[i])
		if expected != "" && expected != uuid {
			return "", errors.New("catalog object has been replaced")
		}
		binding := strings.TrimPrefix(name, "polaris.") + "=" + uuid + version
		if !slices.Contains(bindings, binding) {
			bindings = append(bindings, binding)
		}
		if plan.Kind == "metadata" && len(plan.Access) == 1 && target.Scope == "table" && slices.Contains(maintenanceActions, target.Action) {
			marker := "@maintenance"
			if target.Action == "rewriteDataFiles" {
				marker = "@compaction"
			}
			bindings = append(bindings, marker+"="+uuid)
		}
		if target.MetadataTable != "" {
			base, ok := metadataBase(target.MetadataTable)
			if !ok || base != name || target.Scope != "table" {
				return "", errors.New("invalid Iceberg metadata relation binding")
			}
			metadata := strings.TrimPrefix(target.MetadataTable, "polaris.") + "=" + uuid + ":metadata"
			if !slices.Contains(bindings, metadata) {
				bindings = append(bindings, metadata)
			}
		}
	}
	if len(bindings) == 0 {
		return "-", nil
	}
	return strings.Join(bindings, ","), nil
}

func (s *Server) waitOperation(ctx context.Context, p Profile, path string) error {
	ticker := time.NewTicker(350 * time.Millisecond)
	defer ticker.Stop()
	for ctx.Err() == nil {
		var status struct {
			State   string          `json:"state"`
			User    string          `json:"sessionUser"`
			Message json.RawMessage `json:"message"`
			Error   json.RawMessage `json:"error"`
		}
		if _, err := s.clients.engine(ctx, p, http.MethodGet, path+"/event", nil, &status); err != nil {
			return err
		}
		if status.User != p.KyuubiUser {
			return errors.New("Kyuubi operation identity does not match the authenticated user")
		}
		switch strings.TrimSuffix(status.State, "_STATE") {
		case "FINISHED":
			return nil
		case "INITIALIZED", "PENDING", "RUNNING", "COMPILED", "UNKNOWN":
		default:
			detail := append(status.Message, status.Error...)
			match := sparkErrorClass.FindSubmatch(detail)
			if len(match) > 1 {
				return fmt.Errorf("Spark execution failed (%s)", match[1])
			}
			return errors.New("Spark execution failed")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
	return ctx.Err()
}

func (s *Server) verifyCatalogBinding(ctx context.Context, p Profile, session string) error {
	for _, config := range [][2]string{
		{"spark.sql.catalog.polaris", "org.sparkconsole.ManagedSparkCatalog"},
		{"spark.sql.catalog.polaris.catalog-impl", "org.sparkconsole.ManagedRESTCatalog"},
		{"spark.sql.catalog.polaris.console-principal-id", s.cfg.PolarisID},
		{"spark.sql.catalog.polaris.scope", "PRINCIPAL_ROLE:" + s.cfg.PolarisRole},
		{"spark.sql.catalog.polaris.uri", s.cfg.Polaris},
		{"spark.sql.catalog.polaris.warehouse", s.cfg.Warehouse},
		{"spark.sql.catalog.polaris.cache-enabled", "false"},
		{"spark.sql.defaultCatalog", "polaris"},
		{"spark.sql.catalog.polaris.header.Polaris-Realm", s.cfg.Realm},
		{"spark.sql.variable.substitute", "false"},
	} {
		if err := s.verifySetting(ctx, p, session, config[0], config[1]); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) verifySetting(ctx context.Context, p Profile, session, key, expected string) error {
	return s.verifySQLValue(ctx, p, session, "SET `"+key+"`", expected, 1)
}

func (s *Server) installTicket(ctx context.Context, p Profile, session, sessionID string, job work, sql, bindings string) error {
	deadline, _ := ctx.Deadline()
	raw := strings.Join([]string{
		sessionID, job.x.ID, job.x.Tenant, job.q.Catalog,
		fingerprint(strings.Join(job.UUIDs, "\n")), fingerprint(sql),
		strconv.FormatInt(deadline.Unix()+15, 10), job.q.statementPlan().description(), bindings,
	}, "\n")
	payload := base64.RawURLEncoding.EncodeToString([]byte(raw))
	mac := hmac.New(sha256.New, []byte(s.cfg.SigningKey))
	mac.Write([]byte("spark-console-driver-v1\n" + payload))
	ticket := payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	var result handle
	_, err := s.clients.engine(ctx, p, http.MethodPost, session+"/operations/statement", map[string]any{
		"statement": "SET spark.sql.sparkconsole.authorizationToken=" + ticket, "runAsync": true,
	}, &result)
	if err != nil || !uuidPattern.MatchString(result.ID) {
		return errors.New("could not install Spark Driver authorization")
	}
	op := "/operations/" + result.ID
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		_, _ = s.clients.engine(cleanup, p, http.MethodPut, op, map[string]string{"action": "close"}, nil)
	}()
	if err := s.waitOperation(ctx, p, op); err != nil {
		return errors.New("Spark Driver rejected session authorization")
	}
	return nil
}

func (s *Server) verifySQLValue(ctx context.Context, p Profile, session, statement, expected string, column int) error {
	var result handle
	_, err := s.clients.engine(ctx, p, http.MethodPost, session+"/operations/statement", map[string]any{
		"statement": statement, "runAsync": false, "queryTimeout": 180,
	}, &result)
	if err != nil || !uuidPattern.MatchString(result.ID) {
		return errors.New("Kyuubi SQL catalog policy is unavailable")
	}
	op := "/operations/" + result.ID
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		_, _ = s.clients.engine(cleanup, p, http.MethodPut, op, map[string]string{"action": "close"}, nil)
	}()
	var values engineResult
	_, err = s.clients.engine(ctx, p, http.MethodGet, op+"/rowset?maxrows=1&fetchorientation=FETCH_NEXT", nil, &values)
	var value string
	if err != nil || len(values.Rows) != 1 || len(values.Rows[0].Fields) <= column ||
		json.Unmarshal(values.Rows[0].Fields[column].Value, &value) != nil || value != expected {
		return errors.New("Kyuubi SQL catalog policy does not match the configured user")
	}
	return nil
}

func (s *Server) savePages(ctx context.Context, p Profile, path string, q query) (query, error) {
	used := 0
	page := resultPage{Rows: [][]json.RawMessage{}}
	pageBytes := 0
	flush := func() error {
		if len(page.Rows) == 0 {
			return nil
		}
		raw, err := json.Marshal(page)
		if err == nil {
			err = s.clients.put(ctx, q.ID, q.Pages+1, raw)
		}
		if err != nil {
			return errors.New("could not store query result page")
		}
		q.Pages++
		page.Rows = page.Rows[:0]
		pageBytes = 0
		return nil
	}
	for !q.Truncated {
		var batch engineResult
		if _, err := s.clients.engine(ctx, p, http.MethodGet, path+"/rowset?maxrows=100&fetchorientation=FETCH_NEXT", nil, &batch); err != nil {
			return q, err
		}
		if len(batch.Rows) == 0 {
			break
		}
		if len(batch.Rows) > 100 {
			return q, errors.New("invalid Kyuubi fetch batch")
		}
		for _, row := range batch.Rows {
			if len(row.Fields) != len(q.Columns) {
				return q, errors.New("invalid Kyuubi result row")
			}
			values := make([]json.RawMessage, len(row.Fields))
			for i, f := range row.Fields {
				if len(f.Value) == 0 {
					f.Value = json.RawMessage("null")
				}
				// Preserve numeric precision across JavaScript JSON parsing.
				if (f.Type == "I64_VAL" || strings.HasPrefix(f.Type, "DECIMAL")) &&
					f.Value[0] != '"' && string(f.Value) != "null" {
					f.Value, _ = json.Marshal(string(f.Value))
				}
				values[i] = f.Value
			}
			data, err := json.Marshal(values)
			if err != nil || len(data) > 128<<10 {
				return q, fmt.Errorf("result row exceeds 128 KiB")
			}
			if q.Rows >= s.cfg.MaxRows || used+len(data)+1 > s.cfg.MaxBytes {
				q.Truncated = true
				break
			}
			if len(page.Rows) == 200 || pageBytes+len(data) > 1<<20 {
				if err := flush(); err != nil {
					return q, err
				}
			}
			page.Rows = append(page.Rows, values)
			q.Rows++
			used += len(data) + 1
			pageBytes += len(data) + 1
		}
	}
	return q, flush()
}
