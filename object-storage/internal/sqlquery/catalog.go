package sqlquery

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

func (s catalogSession) namespaceRoot() string { return "/v1/" + s.prefix + "/namespaces" }

func namespaceParts(name string) []string { return strings.Split(name, ".")[1:] }

func (s catalogSession) namespacePath(name string) string {
	return s.namespaceRoot() + "/" + url.PathEscape(strings.Join(namespaceParts(name), "\x1f"))
}

func (s catalogSession) tablePath(name string) string {
	return s.objectPath(name, "table")
}

func (s catalogSession) objectPath(name, kind string) string {
	ns := namespaceOf(name)
	return s.namespacePath(ns) + "/" + kind + "s/" + url.PathEscape(strings.TrimPrefix(name, ns+"."))
}

func (s catalogSession) namespaces(ctx context.Context, parent string) ([]string, error) {
	out := []string{}
	page := url.Values{"pageSize": {"100"}}
	if parent != "" {
		page.Set("parent", strings.Join(namespaceParts(parent), "\x1f"))
	}
	for pages := 0; pages < 10 && len(out) < 1000; pages++ {
		var result struct {
			Namespaces [][]string `json:"namespaces"`
			Next       string     `json:"next-page-token"`
		}
		if err := s.get(ctx, s.namespaceRoot()+"?"+page.Encode(), &result); err != nil {
			return nil, err
		}
		for _, parts := range result.Namespaces {
			name := "polaris." + strings.Join(parts, ".")
			if validNamespace(name) {
				out = append(out, name)
			}
		}
		if result.Next == "" {
			return out, nil
		}
		if result.Next == page.Get("pageToken") || len(result.Namespaces) == 0 {
			break
		}
		page.Set("pageToken", result.Next)
	}
	return nil, errors.New("too many namespaces for one catalog request")
}

func (s catalogSession) objects(ctx context.Context, namespace, kind string) ([]string, error) {
	var out []string
	page := url.Values{"pageSize": {"100"}}
	for pages := 0; pages < 50 && len(out) < 5000; pages++ {
		var result struct {
			Identifiers []struct {
				Namespace []string `json:"namespace"`
				Name      string   `json:"name"`
			} `json:"identifiers"`
			Next string `json:"next-page-token"`
		}
		if err := s.get(ctx, s.namespacePath(namespace)+"/"+kind+"s?"+page.Encode(), &result); err != nil {
			return nil, err
		}
		for _, item := range result.Identifiers {
			name := namespace + "." + item.Name
			if validTable(name) && strings.Join(item.Namespace, ".") == strings.TrimPrefix(namespace, "polaris.") {
				out = append(out, name)
			}
		}
		if result.Next == "" {
			return out, nil
		}
		if result.Next == page.Get("pageToken") || len(result.Identifiers) == 0 {
			break
		}
		page.Set("pageToken", result.Next)
	}
	return nil, errors.New("too many catalog objects for one namespace request")
}

func (s *Server) namespaces(w http.ResponseWriter, r *http.Request, x user) {
	parent := r.URL.Query().Get("parent")
	if parent != "" && !validNamespace(parent) {
		reply(w, 400, nil, "invalid namespace")
		return
	}
	policy, err := s.grantsFor(r.Context(), x, false)
	if err != nil {
		reply(w, 503, nil, "SQL permissions unavailable")
		return
	}
	result := []string{}
	if _, full := permits(policy, "catalog", "polaris", "metadataRead"); full {
		ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
		defer cancel()
		catalog, err := s.clients.catalog(ctx)
		if err == nil {
			result, err = catalog.namespaces(ctx, parent)
		}
		if err != nil {
			reply(w, 503, nil, "Polaris namespace list unavailable")
			return
		}
	} else {
		prefix := "polaris."
		if parent != "" {
			prefix = parent + "."
		}
		for _, g := range policy {
			if !slices.Contains(g.Actions, "metadataRead") {
				continue
			}
			name := g.Table
			if g.Scope == "table" || g.Scope == "view" {
				name = namespaceOf(name)
			}
			if strings.HasPrefix(name, prefix) {
				next, _, _ := strings.Cut(strings.TrimPrefix(name, prefix), ".")
				result = append(result, prefix+next)
			}
		}
	}
	slices.Sort(result)
	reply(w, 200, slices.Compact(result), "")
}

func (s *Server) tables(w http.ResponseWriter, r *http.Request, x user) {
	s.listObjects(w, r, x, "table")
}

func (s *Server) views(w http.ResponseWriter, r *http.Request, x user) {
	s.listObjects(w, r, x, "view")
}

func (s *Server) listObjects(w http.ResponseWriter, r *http.Request, x user, kind string) {
	ns := r.URL.Query().Get("namespace")
	if !validNamespace(ns) {
		reply(w, 400, nil, "select a qualified namespace")
		return
	}
	policy, err := s.grantsFor(r.Context(), x, false)
	if err != nil {
		reply(w, 503, nil, "SQL permissions unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	names := []string{}
	if _, whole := permits(policy, "namespace", ns, "metadataRead"); whole {
		catalog, err := s.clients.catalog(ctx)
		if err == nil {
			names, err = catalog.objects(ctx, ns, kind)
		}
		if err != nil {
			reply(w, 503, nil, "Polaris "+kind+" list unavailable")
			return
		}
	} else {
		for _, g := range policy {
			if g.Scope == kind && namespaceOf(g.Table) == ns && slices.Contains(g.Actions, "metadataRead") {
				names = append(names, g.Table)
			}
		}
	}
	items := make([]grant, 0, len(names))
	for _, name := range names {
		items = append(items, grant{Table: name, Scope: kind})
	}
	reply(w, 200, items, "")
}

type viewInfo struct {
	UUID           string `json:"view-uuid"`
	CurrentVersion int    `json:"current-version-id"`
	Schemas        []struct {
		ID     int `json:"schema-id"`
		Fields []struct {
			Name     string `json:"name"`
			Type     any    `json:"type"`
			Required bool   `json:"required"`
		} `json:"fields"`
	} `json:"schemas"`
	Versions []struct {
		ID              int      `json:"version-id"`
		SchemaID        int      `json:"schema-id"`
		DefaultCatalog  string   `json:"default-catalog"`
		Namespace       []string `json:"default-namespace"`
		Representations []struct {
			Type, Dialect, SQL string
		} `json:"representations"`
	} `json:"versions"`
}

func (s catalogSession) view(ctx context.Context, name string) (viewInfo, error) {
	var out struct {
		Metadata viewInfo `json:"metadata"`
	}
	err := s.get(ctx, s.objectPath(name, "view"), &out)
	if err == nil && !uuidPattern.MatchString(out.Metadata.UUID) {
		err = errors.New("view identity missing")
	}
	return out.Metadata, err
}

func (v viewInfo) sparkDefinition() (string, string) {
	for _, version := range v.Versions {
		if version.ID != v.CurrentVersion || (version.DefaultCatalog != "" && version.DefaultCatalog != "polaris") {
			continue
		}
		ns := "polaris." + strings.Join(version.Namespace, ".")
		if !validNamespace(ns) {
			ns = ""
		}
		for _, representation := range version.Representations {
			if representation.Type == "sql" && strings.EqualFold(representation.Dialect, "spark") {
				return representation.SQL, ns
			}
		}
	}
	return "", ""
}

func (s *Server) view(w http.ResponseWriter, r *http.Request, x user) {
	name := r.URL.Query().Get("name")
	if !validTable(name) {
		reply(w, 400, nil, "invalid view")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	policy, err := s.grantsFor(ctx, x, false)
	g, allowed := permits(policy, "view", name, "metadataRead")
	if err != nil || !allowed {
		reply(w, 404, nil, "view unavailable")
		return
	}
	catalog, err := s.clients.catalog(ctx)
	if err != nil {
		reply(w, 503, nil, "Polaris catalog unavailable")
		return
	}
	info, err := catalog.view(ctx, name)
	if err != nil || (g.UUID != "" && g.UUID != info.UUID) {
		reply(w, 404, nil, "view unavailable or replaced")
		return
	}
	reply(w, 200, info, "")
}

func (s *Server) table(w http.ResponseWriter, r *http.Request, x user) {
	name := r.URL.Query().Get("name")
	if !validTable(name) {
		reply(w, 400, nil, "invalid table")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	permissions, err := s.grantsFor(ctx, x, false)
	g, allowed := permits(permissions, "table", name, "metadataRead")
	if err != nil || !allowed {
		reply(w, 404, nil, "table unavailable")
		return
	}
	session, err := s.clients.catalog(ctx)
	if err != nil {
		reply(w, 503, nil, "Polaris catalog unavailable")
		return
	}
	info, err := session.table(ctx, name)
	if err != nil || (g.UUID != "" && info.UUID != g.UUID) {
		reply(w, 404, nil, "table unavailable or replaced")
		return
	}
	reply(w, 200, info, "")
}

type catalogChange struct {
	Operation string `json:"operation"`
	Scope     string `json:"scope"`
	Name      string `json:"name"`
	Comment   string `json:"comment"`
	Fields    []struct {
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"fields"`
}

func (s *Server) mutateCatalog(w http.ResponseWriter, r *http.Request, x user) {
	var change catalogChange
	if !decode(w, r, &change) {
		return
	}
	if !(change.Scope == "table" && validTable(change.Name) || change.Scope == "namespace" && validNamespace(change.Name)) || len(change.Comment) > 1024 {
		reply(w, 400, nil, "invalid catalog resource")
		return
	}
	permission := change.Operation
	scope, resource := change.Scope, change.Name
	switch change.Operation {
	case "create":
		scope, resource = "namespace", namespaceOf(change.Name)
		if resource == "polaris" {
			scope = "catalog"
		}
	case "delete":
	case "comment":
		permission = "metadataWrite"
	default:
		reply(w, 400, nil, "unsupported catalog action")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 18*time.Second)
	defer cancel()
	grants, err := s.grantsFor(ctx, x, false)
	g, permitted := permits(grants, scope, resource, permission)
	if err != nil || !permitted {
		reply(w, 403, nil, "catalog permission denied")
		return
	}
	catalog, err := s.clients.catalog(ctx)
	if err != nil {
		reply(w, 503, nil, "Polaris catalog unavailable")
		return
	}
	method, endpoint, body, err := catalog.planChange(ctx, change, g.UUID)
	if err != nil {
		reply(w, 409, nil, err.Error())
		return
	}
	action := "SQL_CATALOG_" + strings.ToUpper(change.Operation)
	db, err := s.db.Begin(ctx)
	if err != nil {
		reply(w, 503, nil, "SQL audit unavailable")
		return
	}
	defer db.Rollback(ctx)
	if err := audit(ctx, db, x, change.Name, action+"_REQUEST", "SUCCESS", change.Scope); err != nil || db.Commit(ctx) != nil {
		reply(w, 503, nil, "SQL audit unavailable")
		return
	}
	err = catalog.call(ctx, method, endpoint, body, nil)
	result := "SUCCESS"
	if err != nil {
		result = "FAILED"
	}
	auditCtx, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	if tx, e := s.db.Begin(auditCtx); e == nil {
		defer tx.Rollback(auditCtx)
		if e = audit(auditCtx, tx, x, change.Name, action, result, change.Scope); e == nil {
			e = tx.Commit(auditCtx)
		}
		dbError(e)
	}
	if err != nil {
		reply(w, 409, nil, err.Error())
		return
	}
	reply(w, 200, map[string]bool{"applied": true}, "")
}

func (s catalogSession) planChange(ctx context.Context, change catalogChange, expected string) (string, string, any, error) {
	name := change.Name
	if change.Scope == "namespace" {
		switch change.Operation {
		case "create":
			return http.MethodPost, s.namespaceRoot(), map[string]any{"namespace": namespaceParts(name), "properties": map[string]string{"comment": change.Comment}}, nil
		case "delete":
			return http.MethodDelete, s.namespacePath(name), nil, nil
		default:
			return http.MethodPost, s.namespacePath(name) + "/properties", map[string]any{"updates": map[string]string{"comment": change.Comment}}, nil
		}
	}
	if change.Operation == "create" {
		if len(change.Fields) == 0 || len(change.Fields) > 100 {
			return "", "", nil, errors.New("table requires one to 100 fields")
		}
		fields := []map[string]any{}
		seen := map[string]bool{}
		for i, field := range change.Fields {
			if !safeName.MatchString(field.Name) || seen[field.Name] || !slices.Contains([]string{"boolean", "int", "long", "float", "double", "date", "timestamp", "timestamptz", "string", "binary"}, field.Type) {
				return "", "", nil, errors.New("invalid or duplicate Iceberg field")
			}
			seen[field.Name] = true
			fields = append(fields, map[string]any{"id": i + 1, "name": field.Name, "type": field.Type, "required": false})
		}
		ns := namespaceOf(name)
		return http.MethodPost, s.namespacePath(ns) + "/tables", map[string]any{
			"name":       strings.TrimPrefix(name, ns+"."),
			"schema":     map[string]any{"type": "struct", "schema-id": 0, "fields": fields},
			"properties": map[string]string{"comment": change.Comment},
		}, nil
	}
	info, err := s.table(ctx, name)
	if err != nil || (expected != "" && info.UUID != expected) {
		return "", "", nil, errors.New("table unavailable or replaced")
	}
	if change.Operation == "delete" {
		return http.MethodDelete, s.tablePath(name) + "?purgeRequested=false", nil, nil
	}
	return http.MethodPost, s.tablePath(name), map[string]any{
		"requirements": []map[string]string{{"type": "assert-table-uuid", "uuid": info.UUID}},
		"updates":      []map[string]any{{"action": "set-properties", "updates": map[string]string{"comment": change.Comment}}},
	}, nil
}
