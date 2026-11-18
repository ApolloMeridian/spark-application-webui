package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"
)

var sqlProxyTransport = func() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = nil
	t.ResponseHeaderTimeout = 25 * time.Second
	return t
}()

func (s *Server) sqlProxy(w http.ResponseWriter, r *http.Request) {
	if !s.config.SQLEnabled {
		s.writeError(w, http.StatusNotFound, errors.New("SQL is disabled"))
		return
	}
	user := currentUser(r)
	if user.ID == "" || user.TenantID == "" || !s.requirePermission(w, user, requestCapability(r)) {
		if user.ID == "" || user.TenantID == "" {
			s.writeError(w, http.StatusForbidden, errors.New("SQL requires an authenticated tenant identity"))
		}
		return
	}
	target, err := url.Parse(s.config.SQLURL)
	if err != nil || target.Host == "" {
		s.writeError(w, http.StatusServiceUnavailable, errors.New("SQL service unavailable"))
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	}
	proxy := &httputil.ReverseProxy{
		Transport: sqlProxyTransport,
		Rewrite: func(req *httputil.ProxyRequest) {
			req.SetURL(target)
			out := req.Out
			out.URL.Path = strings.Replace(r.URL.Path, "/v1/sql/", "/v1/", 1)
			out.URL.RawPath = ""
			for header := range out.Header {
				if strings.HasPrefix(strings.ToLower(header), "x-spark-") {
					out.Header.Del(header)
				}
			}
			out.Header.Del("Cookie")
			out.Header.Del("Authorization")
			claims, _ := json.Marshal(map[string]any{
				"id": user.ID, "tenant": user.TenantID, "method": out.Method,
				"target": out.URL.RequestURI(), "aud": "spark-console-sql", "exp": time.Now().Add(45 * time.Second).Unix(),
			})
			payload := base64.RawURLEncoding.EncodeToString(claims)
			mac := hmac.New(sha256.New, []byte(s.config.SQLSigningKey))
			mac.Write([]byte(payload))
			out.Header.Set("X-Spark-SQL-Identity", payload+"."+base64.RawURLEncoding.EncodeToString(mac.Sum(nil)))
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			s.writeError(w, http.StatusServiceUnavailable, errors.New("SQL service unavailable"))
		},
	}
	proxy.ServeHTTP(w, r)
}
