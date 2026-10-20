package api

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"spark-control-center/backend/internal/domain"
)

func (s *Server) driverLogs(w http.ResponseWriter, r *http.Request) {
	if !s.requireNamespace(w, currentUser(r), r.PathValue("namespace")) {
		return
	}
	values := r.URL.Query()
	q := domain.DriverLogQuery{Source: values.Get("source"), Mode: values.Get("mode"), Direction: values.Get("direction"), Limit: 2000}
	if q.Source == "" {
		q.Source = "kubernetes"
	}
	if q.Mode == "" {
		q.Mode = "tail"
	}
	if q.Direction == "" {
		q.Direction = "forward"
	}
	valid := (q.Source == "kubernetes" || q.Source == "loki") && (q.Mode == "tail" || q.Mode == "range") && (q.Direction == "forward" || q.Direction == "backward")
	for key, target := range map[string]*int{"limit": &q.Limit, "startLine": &q.StartLine, "endLine": &q.EndLine} {
		if values.Has(key) {
			value, err := strconv.Atoi(values.Get(key))
			if err != nil || value < 1 {
				valid = false
			} else {
				*target = value
			}
		}
	}
	if q.Source == "kubernetes" && q.Mode == "range" {
		valid = valid && q.StartLine > 0 && q.EndLine >= q.StartLine && q.EndLine-q.StartLine < domain.MaxDriverLogLines
		if valid {
			q.Limit = q.EndLine - q.StartLine + 1
		}
	}
	if q.Source == "loki" {
		from, e1 := time.Parse(time.RFC3339Nano, values.Get("from"))
		to, e2 := time.Parse(time.RFC3339Nano, values.Get("to"))
		valid = valid && e1 == nil && e2 == nil && from.Before(to)
		q.From, q.To = values.Get("from"), values.Get("to")
	}
	if !valid || q.Limit < 1 || q.Limit > domain.MaxDriverLogLines {
		s.writeError(w, 400, fmt.Errorf("invalid log query: use a valid source/mode, positive line range of at most 10000 lines, or an increasing Loki time range"))
		return
	}
	reader, ok := s.service.(interface {
		GetDriverLogs(context.Context, string, string, domain.DriverLogQuery) (domain.DriverLogWindow, error)
	})
	if !ok {
		s.writeError(w, 503, fmt.Errorf("Driver logs unavailable"))
		return
	}
	out, err := reader.GetDriverLogs(r.Context(), r.PathValue("namespace"), r.PathValue("name"), q)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	s.writeJSON(w, 200, out)
}
