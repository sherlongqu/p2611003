package main

import (
	"crypto/subtle"
	"errors"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

func (a *App) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if a.isAdmin(r) {
		http.Redirect(w, r, "/studio/admin", http.StatusSeeOther)
		return
	}
	base := a.privateBase(r, "")
	base.Title = "Studio sign in · " + a.cfg.SiteName
	a.render(w, http.StatusOK, "login", loginData{baseData: base, Next: safeNext(r.URL.Query().Get("next"))})
}

func (a *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "cross-site request blocked", http.StatusForbidden)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	ip := clientIP(r)
	base := a.privateBase(r, "")
	base.Title = "Studio sign in · " + a.cfg.SiteName
	next := safeNext(r.PostFormValue("next"))

	if a.loginBlocked(ip) {
		a.render(w, http.StatusTooManyRequests, "login", loginData{baseData: base, Error: "Too many attempts. Try again in 10 minutes.", Next: next})
		return
	}
	key := strings.TrimSpace(r.PostFormValue("key"))
	if key == "" || subtle.ConstantTimeCompare([]byte(key), []byte(a.cfg.APIKey)) != 1 {
		a.noteLoginFail(ip)
		time.Sleep(350 * time.Millisecond)
		a.render(w, http.StatusUnauthorized, "login", loginData{baseData: base, Error: "Incorrect key. Please try again.", Next: next})
		return
	}

	a.clearLoginFails(ip)
	a.setSession(w, r)
	if next == "" {
		next = "/studio/admin"
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (a *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "cross-site request blocked", http.StatusForbidden)
		return
	}
	a.clearSession(w)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func safeNext(next string) string {
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.Contains(next, "\\") {
		return ""
	}
	u, err := url.Parse(next)
	if err != nil || u.Scheme != "" || u.Host != "" {
		return ""
	}
	return next
}

func (a *App) handleUploadPage(w http.ResponseWriter, r *http.Request) {
	base := a.privateBase(r, "upload")
	base.Title = "Upload documents · " + a.cfg.SiteName
	a.render(w, http.StatusOK, "upload", uploadData{baseData: base, MaxMB: a.cfg.MaxUploadMB})
}

func (a *App) handleUploadForm(w http.ResponseWriter, r *http.Request) {
	doc, _, err := a.receiveUpload(w, r)
	if err != nil {
		status, msg := uploadError(err)
		base := a.privateBase(r, "upload")
		base.Title = "Upload documents · " + a.cfg.SiteName
		a.render(w, status, "upload", uploadData{baseData: base, MaxMB: a.cfg.MaxUploadMB, Error: msg})
		return
	}
	http.Redirect(w, r, "/d/"+doc.ID, http.StatusSeeOther)
}

func (a *App) handleAdmin(w http.ResponseWriter, r *http.Request) {
	base := a.privateBase(r, "")
	base.Title = "Studio · " + a.cfg.SiteName
	hint := ""
	if n := len(a.cfg.APIKey); n >= 4 {
		hint = "…" + a.cfg.APIKey[n-4:]
	}
	a.render(w, http.StatusOK, "admin", adminData{
		baseData: base,
		Docs:     a.store.List("", "", "new"),
		Stats:    a.store.Stats(),
		KeyHint:  hint,
		APIBase:  base.BaseURL,
	})
}

func (a *App) handleAdminReplace(w http.ResponseWriter, r *http.Request) {
	p, err := a.readUpload(w, r)
	if err != nil {
		log.Printf("replace %s: %v", r.PathValue("id"), err)
		http.Redirect(w, r, "/studio/admin", http.StatusSeeOther)
		return
	}
	defer p.Close()
	reader, _, err := a.prepareDocument(p)
	if err != nil {
		log.Printf("replace %s: %v", r.PathValue("id"), err)
		http.Redirect(w, r, "/studio/admin", http.StatusSeeOther)
		return
	}
	if _, err := a.store.Replace(r.PathValue("id"), reader, p.filename, "", "", nil, p.maxBytes); err != nil {
		log.Printf("replace %s: %v", r.PathValue("id"), err)
	}
	http.Redirect(w, r, "/studio/admin", http.StatusSeeOther)
}

func (a *App) handleAdminDelete(w http.ResponseWriter, r *http.Request) {
	if err := a.store.Delete(r.PathValue("id")); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("delete: %v", err)
		http.Error(w, "delete failed", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/studio/admin", http.StatusSeeOther)
}
