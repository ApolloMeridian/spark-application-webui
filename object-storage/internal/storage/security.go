package storage

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type Claims struct {
	ID       string `json:"id"`
	Tenant   string `json:"tenant"`
	Method   string `json:"method"`
	Target   string `json:"target"`
	Audience string `json:"aud"`
	Expires  int64  `json:"exp"`
}

func Sign(key []byte, claims Claims) string {
	raw, _ := json.Marshal(claims)
	payload := base64.RawURLEncoding.EncodeToString(raw)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func Verify(key []byte, r *http.Request) (Claims, error) {
	var x Claims
	parts := strings.Split(r.Header.Get("X-Spark-Storage-Identity"), ".")
	if len(parts) != 2 {
		return x, fail(401, "invalid_internal_identity")
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(parts[0]))
	sig, e := base64.RawURLEncoding.DecodeString(parts[1])
	if e != nil || !hmac.Equal(sig, mac.Sum(nil)) {
		return x, fail(401, "invalid_internal_identity")
	}
	raw, e := base64.RawURLEncoding.DecodeString(parts[0])
	if e != nil || json.Unmarshal(raw, &x) != nil {
		return x, fail(401, "invalid_internal_identity")
	}
	now := time.Now().Unix()
	if x.ID == "" || x.Tenant == "" || x.Audience != "spark-object-storage" || x.Expires < now || x.Expires > now+90 || x.Method != r.Method || x.Target != r.URL.RequestURI() {
		return x, fail(401, "invalid_internal_identity")
	}
	return x, nil
}
func ValidKey(key string) bool {
	if !utf8.ValidString(key) || len(key) > 1024 || strings.HasPrefix(key, "/") || strings.Contains(key, "\\") {
		return false
	}
	for _, r := range key {
		if unicode.IsControl(r) {
			return false
		}
	}
	for _, segment := range strings.Split(key, "/") {
		if segment == "." || segment == ".." {
			return false
		}
	}
	return true
}
func ValidPrefix(prefix string) bool {
	return ValidKey(prefix) && (prefix == "" || strings.HasSuffix(prefix, "/"))
}
func Allows(user Identity, grants []Grant, connection, bucket, key, action string) bool {
	if !ValidKey(key) {
		return false
	}
	for _, g := range grants {
		if g.UserID != user.ID || g.Tenant != user.Tenant || g.Connection != connection || g.Bucket != bucket || !ValidPrefix(g.Prefix) || !strings.HasPrefix(key, g.Prefix) {
			continue
		}
		for _, a := range g.Actions {
			if a == action {
				return true
			}
		}
	}
	return false
}

type Ticket struct {
	User       string `json:"user"`
	Tenant     string `json:"tenant"`
	Connection string `json:"connection"`
	Bucket     string `json:"bucket"`
	Key        string `json:"key"`
	Kind       string `json:"kind"`
	Operation  string `json:"operation"`
	Upload     Upload `json:"upload"`
	Cursor     string `json:"cursor"`
	Search     string `json:"search,omitempty"`
	Policy     string `json:"policy"`
	Expires    int64  `json:"exp"`
}

func seal(key []byte, x Ticket) (string, error) {
	sum := sha256.Sum256(key)
	block, e := aes.NewCipher(sum[:])
	if e != nil {
		return "", e
	}
	aead, e := cipher.NewGCM(block)
	if e != nil {
		return "", e
	}
	nonce := make([]byte, aead.NonceSize())
	if _, e = rand.Read(nonce); e != nil {
		return "", e
	}
	raw, e := json.Marshal(x)
	if e != nil {
		return "", e
	}
	return base64.RawURLEncoding.EncodeToString(aead.Seal(nonce, nonce, raw, []byte("spark-storage-ticket-v1"))), nil
}
func unseal(key []byte, value string, user Identity, kind string) (Ticket, error) {
	var x Ticket
	sum := sha256.Sum256(key)
	block, _ := aes.NewCipher(sum[:])
	aead, _ := cipher.NewGCM(block)
	raw, e := base64.RawURLEncoding.DecodeString(value)
	if e != nil || len(raw) < aead.NonceSize() || len(raw) > 65536 {
		return x, fail(400, "invalid_ticket")
	}
	raw, e = aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], []byte("spark-storage-ticket-v1"))
	if e != nil || json.Unmarshal(raw, &x) != nil || x.User != user.ID || x.Tenant != user.Tenant || x.Kind != kind || x.Expires < time.Now().Unix() {
		return x, fail(400, "invalid_ticket")
	}
	return x, nil
}
