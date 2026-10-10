package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"
)

// This gateway deliberately has no storage SDK and never buffers file bodies.
var storageProxyTransport = func() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.ResponseHeaderTimeout = 30 * time.Second
	return t
}()

func (s *Server) objectStorageProxy(w http.ResponseWriter, r *http.Request) {
	if !s.config.ObjectStorageEnabled {
		s.writeError(w, 404, fmt.Errorf("object storage is disabled"))
		return
	}
	target, err := url.Parse(s.config.ObjectStorageURL)
	if err != nil || target.Host == "" {
		s.writeError(w, 503, fmt.Errorf("object storage unavailable"))
		return
	}
	user := currentUser(r)
	tenant := user.TenantID
	if tenant == "" {
		tenant = "default"
	}
	// File streams have their own finite deadline instead of core JSON deadlines.
	deadline := time.Now().Add(time.Hour)
	controller := http.NewResponseController(w)
	_ = controller.SetReadDeadline(deadline)
	_ = controller.SetWriteDeadline(deadline)
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Director = func(out *http.Request) {
		out.URL.Scheme = target.Scheme
		out.URL.Host = target.Host
		out.Host = target.Host
		out.URL.Path = "/v1/" + strings.TrimPrefix(r.URL.Path, "/v1/object-storage/")
		out.URL.RawPath = ""
		for header := range out.Header {
			if strings.HasPrefix(strings.ToLower(header), "x-spark-") || strings.HasPrefix(strings.ToLower(header), "x-forwarded-") {
				out.Header.Del(header)
			}
		}
		out.Header.Del("Cookie")
		out.Header.Del("Authorization")
		claims := struct {
			ID       string `json:"id"`
			Tenant   string `json:"tenant"`
			Method   string `json:"method"`
			Target   string `json:"target"`
			Audience string `json:"aud"`
			Expires  int64  `json:"exp"`
		}{user.ID, tenant, out.Method, out.URL.RequestURI(), "spark-object-storage", time.Now().Add(time.Minute).Unix()}
		raw, _ := json.Marshal(claims)
		payload := base64.RawURLEncoding.EncodeToString(raw)
		mac := hmac.New(sha256.New, []byte(s.config.ObjectStorageSigningKey))
		mac.Write([]byte(payload))
		out.Header.Set("X-Spark-Storage-Identity", payload+"."+base64.RawURLEncoding.EncodeToString(mac.Sum(nil)))
	}
	proxy.Transport = storageProxyTransport
	proxy.FlushInterval = -1
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) {
		s.writeError(w, 503, fmt.Errorf("object storage service unavailable"))
	}
	proxy.ServeHTTP(w, r)
}
