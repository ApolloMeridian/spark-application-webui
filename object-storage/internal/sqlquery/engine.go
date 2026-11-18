package sqlquery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
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

func (s *Server) run(ctx context.Context, job work) {
	ctx, stop := context.WithTimeout(ctx, s.cfg.Timeout)
	defer stop()
	go s.keepLease(ctx, job, stop)
	q, err := s.execute(ctx, job)
	s.finish(job, q, err)
}

func (s *Server) execute(ctx context.Context, job work) (query, error) {
	q := job.q
	p, ok := s.cfg.Profiles[job.x.ID]
	if !ok || p.Tenant != job.x.Tenant {
		return q, errors.New("SQL identity is not provisioned")
	}
	if _, err := s.actualUser(ctx, job.x.actor); err != nil {
		return q, errors.New("SQL access was revoked")
	}
	grants, err := s.authorize(ctx, job.x, q.Tables)
	if err != nil {
		return q, err
	}
	if len(grants) > 0 {
		check, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		catalog, err := s.clients.catalog(check, p)
		if err != nil {
			return q, err
		}
		for i, name := range q.Tables {
			info, err := catalog.table(check, name)
			if err != nil || info.UUID != tableUUID(job.UUIDs[i]) || grants[i] != job.UUIDs[i] {
				return q, errors.New("table access was revoked or the table has been replaced")
			}
		}
	}
	check, cancel := context.WithTimeout(ctx, 8*time.Second)
	err = s.clients.checkKyuubiAuth(check, p)
	cancel()
	if err != nil {
		return q, err
	}
	var session handle
	_, err = s.clients.engine(ctx, p, http.MethodPost, "/sessions", map[string]any{"configs": map[string]string{}}, &session)
	if err != nil || !uuidPattern.MatchString(session.ID) {
		return q, errors.New("could not open Kyuubi SQL session")
	}
	sessionPath := "/sessions/" + session.ID
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		_, _ = s.clients.engine(cleanup, p, http.MethodDelete, sessionPath, nil, nil)
	}()
	if err := s.verifyCatalogBinding(ctx, p, sessionPath); err != nil {
		return q, err
	}
	if _, err := s.actualUser(ctx, job.x.actor); err != nil {
		return q, errors.New("SQL access was revoked")
	}
	grants, err = s.authorize(ctx, job.x, q.Tables)
	if err != nil {
		return q, err
	}
	for i, version := range grants {
		if version != job.UUIDs[i] {
			return q, errors.New("table permission changed")
		}
	}
	var operation handle
	// Add a bounded top-level result limit only after the Spark parser has
	// accepted a single read-only query. A newline terminates any SQL comment.
	sql := "SELECT * FROM (\n" + job.SQL + "\n) AS console_result LIMIT " + strconv.Itoa(s.cfg.MaxRows+1)
	_, err = s.clients.engine(ctx, p, http.MethodPost, sessionPath+"/operations/statement", map[string]any{
		"statement": sql, "runAsync": true, "queryTimeout": int(s.cfg.Timeout.Seconds()),
	}, &operation)
	if err != nil || !uuidPattern.MatchString(operation.ID) {
		return q, errors.New("Kyuubi did not accept the statement")
	}
	q.Operation = operation.ID
	op := "/operations/" + operation.ID
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		if ctx.Err() != nil {
			_, _ = s.clients.engine(cleanup, p, http.MethodPut, op, map[string]string{"action": "cancel"}, nil)
		}
		_, _ = s.clients.engine(cleanup, p, http.MethodPut, op, map[string]string{"action": "close"}, nil)
	}()
	_, err = s.db.Exec(ctx, "UPDATE console_sql_queries SET operation=$2 WHERE id=$1 AND state='running' AND worker_deadline>now()", q.ID, q.Operation)
	if err != nil {
		return q, errors.New("could not record the operation")
	}
	if err := s.waitOperation(ctx, p, op); err != nil {
		return q, err
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

func (s *Server) waitOperation(ctx context.Context, p Profile, path string) error {
	ticker := time.NewTicker(350 * time.Millisecond)
	defer ticker.Stop()
	for ctx.Err() == nil {
		var status struct {
			State string `json:"state"`
			User  string `json:"sessionUser"`
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
			return errors.New("Spark did not complete the query")
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
		{"spark.sql.catalog.polaris.catalog-impl", "org.sparkconsole.MountedRESTCatalog"},
		{"spark.sql.catalog.polaris.console-credential-file", "/var/run/spark-console/polaris/" + p.KyuubiUser + "/credential"},
		{"spark.sql.catalog.polaris.console-principal-id", p.PolarisID},
		{"spark.sql.catalog.polaris.scope", "PRINCIPAL_ROLE:" + p.PolarisRole},
		{"spark.sql.catalog.polaris.uri", s.cfg.Polaris},
		{"spark.sql.catalog.polaris.warehouse", s.cfg.Warehouse},
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
	var result handle
	_, err := s.clients.engine(ctx, p, http.MethodPost, session+"/operations/statement", map[string]any{
		"statement": "SET `" + key + "`", "runAsync": false, "queryTimeout": 180,
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
	if err != nil || len(values.Rows) != 1 || len(values.Rows[0].Fields) != 2 ||
		json.Unmarshal(values.Rows[0].Fields[1].Value, &value) != nil || value != expected {
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
