package api

import (
	"net/http"
	"spark-control-center/backend/internal/domain"
	"spark-control-center/backend/internal/localauth"
	"strings"
)

func requestCapability(r *http.Request) string {
	p := r.URL.Path
	write := r.Method != http.MethodGet && r.Method != http.MethodHead
	switch {
	case strings.HasPrefix(p, "/v1/object-storage"):
		return "objectStorage"
	case strings.HasPrefix(p, "/v1/templates"):
		if write {
			return "templatesWrite"
		}
		return "templatesRead"
	case strings.HasPrefix(p, "/v1/alert"), strings.HasPrefix(p, "/v1/fingerprints"), strings.HasPrefix(p, "/v1/diagnostics/fingerprints"):
		if write {
			return "alertsWrite"
		}
		return "alertsRead"
	case strings.HasPrefix(p, "/v1/projects"), strings.HasPrefix(p, "/v1/workflows"):
		if write {
			return "workflowsWrite"
		}
		return "workflowsRead"
	case strings.HasSuffix(p, "/kill"):
		return "kill"
	case strings.HasSuffix(p, "/prepare"):
		return "submit"
	case write && strings.HasPrefix(p, "/v1/namespaces/"):
		if r.Method == http.MethodDelete {
			return "delete"
		}
		if strings.HasSuffix(p, "/favorite") {
			return ""
		}
		return "submit"
	}
	return ""
}

func (s *Server) requirePermission(w http.ResponseWriter, user domain.User, capability string) bool {
	if !user.Can(capability) {
		s.writeAuthError(w, localauth.ErrForbidden)
		return false
	}
	return true
}
