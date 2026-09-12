package platform

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type Cursor struct {
	V         int       `json:"v"`
	Kind      string    `json:"kind"`
	Tenant    string    `json:"tenant_id"`
	Filter    string    `json:"filter_hash"`
	LastTime  time.Time `json:"last_time"`
	LastID    string    `json:"last_id"`
	UpperTime time.Time `json:"upper_time"`
	Expires   time.Time `json:"expires_at"`
}

func SignCursor(c Cursor, key []byte) (string, error) {
	if len(key) < 32 {
		return "", errors.New("cursor key too short")
	}
	c.V = 1
	b, e := json.Marshal(c)
	if e != nil {
		return "", e
	}
	h := hmac.New(sha256.New, key)
	h.Write(b)
	return base64.RawURLEncoding.EncodeToString(b) + "." + base64.RawURLEncoding.EncodeToString(h.Sum(nil)), nil
}
func ReadCursor(token string, key []byte, kind, tenant, filter string, now time.Time) (Cursor, error) {
	var c Cursor
	bad := errors.New("invalid cursor")
	if len(token) > 2048 || len(key) < 32 {
		return c, bad
	}
	p := strings.Split(token, ".")
	if len(p) != 2 {
		return c, bad
	}
	b, e := base64.RawURLEncoding.DecodeString(p[0])
	if e != nil {
		return c, bad
	}
	sig, e := base64.RawURLEncoding.DecodeString(p[1])
	if e != nil {
		return c, bad
	}
	h := hmac.New(sha256.New, key)
	h.Write(b)
	if !hmac.Equal(sig, h.Sum(nil)) {
		return c, bad
	}
	if json.Unmarshal(b, &c) != nil || c.V != 1 || c.Kind != kind || c.Tenant != tenant || c.Filter != filter || !now.Before(c.Expires) {
		return c, bad
	}
	return c, nil
}
