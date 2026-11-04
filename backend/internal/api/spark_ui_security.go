package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

var sparkUITransport = func() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = nil
	t.ResponseHeaderTimeout = 15 * time.Second
	t.MaxConnsPerHost = 16
	return t
}()

func (s *Server) sparkUIPreflight(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Origin") != "null" || r.Header.Get("Access-Control-Request-Method") != "GET" {
		w.WriteHeader(403)
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", "null")
	w.Header().Set("Access-Control-Allow-Credentials", "true")
	w.Header().Set("Access-Control-Allow-Methods", "GET")
	w.Header().Set("Access-Control-Allow-Headers", "X-Requested-With, Content-Type")
	w.Header().Set("Vary", "Origin, Access-Control-Request-Method, Access-Control-Request-Headers")
	w.WriteHeader(204)
}

// Report the current relative view so the authenticated parent can periodically
// reopen the same page with a fresh read-only ticket, without reading frame DOM.
func sparkUIBootstrap(data []byte, prefix string) []byte {
	root, _ := json.Marshal(prefix)
	script := `<script>(function(){const root=` + string(root) + `;const path=location.pathname.startsWith(root+'/')?location.pathname.slice(root.length+1):'';parent.postMessage({type:'spark-ui-location',path:path+location.search+location.hash},'*');})();</script>`
	if i := bytes.Index(bytes.ToLower(data), []byte("<head>")); i >= 0 {
		return append(append(append([]byte{}, data[:i+6]...), []byte(script)...), data[i+6:]...)
	}
	return append([]byte(script), data...)
}

func sparkUITicketPath(path string) (token, ns, name, rest string) {
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(parts) < 8 || parts[0] != "v1" || parts[1] != "namespaces" || parts[3] != "applications" || parts[5] != "spark-ui" || parts[6] != "t" || parts[7] == "" {
		return
	}
	return parts[7], parts[2], parts[4], strings.Join(parts[8:], "/")
}
func redactSparkUITicket(path string) string {
	marker := "/spark-ui/t/"
	i := strings.Index(path, marker)
	if i < 0 {
		return path
	}
	end := strings.Index(path[i+len(marker):], "/")
	if end < 0 {
		return path[:i] + marker + "[redacted]"
	}
	return path[:i] + marker + "[redacted]" + path[i+len(marker)+end:]
}
