package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const sessionCookie = "pubdocs_admin"

type loginAttempt struct {
	count int
	reset time.Time
}

// apiKeyValid accepts the key through X-API-Key or a Bearer token.
func (a *App) apiKeyValid(r *http.Request) bool {
	key := strings.TrimSpace(r.Header.Get("X-API-Key"))
	if key == "" {
		auth := r.Header.Get("Authorization")
		if len(auth) > 7 && strings.EqualFold(auth[:7], "Bearer ") {
			key = strings.TrimSpace(auth[7:])
		}
	}
	if key == "" || a.cfg.APIKey == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(key), []byte(a.cfg.APIKey)) == 1
}

func (a *App) sessionToken(exp int64) string {
	mac := hmac.New(sha256.New, a.sessionSecret)
	fmt.Fprintf(mac, "pubdocs-session-v1:%d", exp)
	return strconv.FormatInt(exp, 10) + "." + hex.EncodeToString(mac.Sum(nil))
}

func (a *App) validSession(r *http.Request) bool {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return false
	}
	parts := strings.SplitN(c.Value, ".", 2)
	if len(parts) != 2 {
		return false
	}
	exp, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || time.Now().Unix() >= exp {
		return false
	}
	expected := a.sessionToken(exp)
	return subtle.ConstantTimeCompare([]byte(c.Value), []byte(expected)) == 1
}

func (a *App) isAdmin(r *http.Request) bool {
	return a.apiKeyValid(r) || a.validSession(r)
}

func (a *App) setSession(w http.ResponseWriter, r *http.Request) {
	exp := time.Now().Add(7 * 24 * time.Hour).Unix()
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    a.sessionToken(exp),
		Path:     "/",
		MaxAge:   7 * 24 * 60 * 60,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   isHTTPS(r),
	})
}

func (a *App) clearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// sameOrigin blocks cross-site form posts that ride on the session cookie.
func sameOrigin(r *http.Request) bool {
	if site := r.Header.Get("Sec-Fetch-Site"); site == "cross-site" {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

func isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func requestBaseURL(r *http.Request) string {
	scheme := "http"
	if isHTTPS(r) {
		scheme = "https"
	}
	host := r.Host
	if host == "" {
		host = "localhost"
	}
	return scheme + "://" + host
}

func clientIP(r *http.Request) string {
	if ip := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); ip != "" && net.ParseIP(ip) != nil {
		return ip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// loginBlocked throttles repeated bad passwords from one address.
func (a *App) loginBlocked(ip string) bool {
	a.loginMu.Lock()
	defer a.loginMu.Unlock()
	att, ok := a.loginFails[ip]
	if !ok {
		return false
	}
	if time.Now().After(att.reset) {
		delete(a.loginFails, ip)
		return false
	}
	return att.count >= 8
}

func (a *App) noteLoginFail(ip string) {
	a.loginMu.Lock()
	defer a.loginMu.Unlock()
	now := time.Now()
	if len(a.loginFails) > 2048 {
		for k, v := range a.loginFails {
			if now.After(v.reset) {
				delete(a.loginFails, k)
			}
		}
	}
	att, ok := a.loginFails[ip]
	if !ok || now.After(att.reset) {
		a.loginFails[ip] = &loginAttempt{count: 1, reset: now.Add(10 * time.Minute)}
		return
	}
	att.count++
}

func (a *App) clearLoginFails(ip string) {
	a.loginMu.Lock()
	defer a.loginMu.Unlock()
	delete(a.loginFails, ip)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"ok": false, "error": msg})
}
