package main

import (
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
)

func (a *App) handleAPIDocs(w http.ResponseWriter, r *http.Request) {
	base := a.baseData(r, "api")
	base.Title = "API · " + a.cfg.SiteName
	base.MetaDescription = "Publish and manage public documents with the ArchHub Public HTTP API."
	a.render(w, http.StatusOK, "api", base)
}

func (a *App) handleAPIUpload(w http.ResponseWriter, r *http.Request) {
	if !a.isAdmin(r) {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if !a.apiKeyValid(r) && !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "cross-site request blocked")
		return
	}
	doc, rep, err := a.receiveUpload(w, r)
	if err != nil {
		status, msg := uploadError(err)
		writeError(w, status, msg)
		return
	}
	payload := map[string]any{
		"ok":       true,
		"document": apiDocument{Document: doc, URL: a.publicBaseURL(r) + "/d/" + doc.ID},
	}
	if rep != nil {
		payload["inline"] = rep
	}
	writeJSON(w, http.StatusCreated, payload)
}

func (a *App) handleAPIReplace(w http.ResponseWriter, r *http.Request) {
	if !a.isAdmin(r) {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if !a.apiKeyValid(r) && !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "cross-site request blocked")
		return
	}
	doc, rep, err := a.receiveReplace(w, r, r.PathValue("id"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		status, msg := uploadError(err)
		writeError(w, status, msg)
		return
	}
	payload := map[string]any{
		"ok":       true,
		"replaced": doc.ID,
		"document": apiDocument{Document: doc, URL: a.publicBaseURL(r) + "/d/" + doc.ID},
	}
	if rep != nil {
		payload["inline"] = rep
	}
	writeJSON(w, http.StatusOK, payload)
}

func (a *App) handleAPIList(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if tag := strings.TrimSpace(r.URL.Query().Get("tag")); tag != "" {
		q = "#" + tag
	}
	kind := normalizeKind(r.URL.Query().Get("type"))
	sortBy := r.URL.Query().Get("sort")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 500 {
		limit = 100
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}

	docs := a.store.List(q, kind, sortBy)
	total := len(docs)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	out := make([]apiDocument, 0, end-offset)
	baseURL := a.publicBaseURL(r)
	for _, d := range docs[offset:end] {
		out = append(out, apiDocument{Document: d, URL: baseURL + "/d/" + d.ID})
	}
	stats := a.store.Stats()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"total":     total,
		"count":     len(out),
		"documents": out,
		"stats": map[string]any{
			"documents": stats.Count,
			"bytes":     stats.Bytes,
			"latest":    stats.Latest,
		},
	})
}

func (a *App) handleAPIGet(w http.ResponseWriter, r *http.Request) {
	doc, ok := a.store.Get(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"document": apiDocument{Document: doc, URL: a.publicBaseURL(r) + "/d/" + doc.ID},
	})
}

func (a *App) handleAPIVersions(w http.ResponseWriter, r *http.Request) {
	doc, ok := a.store.Get(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "versions": doc.Versions})
}

func (a *App) handleAPIDelete(w http.ResponseWriter, r *http.Request) {
	if !a.isAdmin(r) {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if !a.apiKeyValid(r) && !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "cross-site request blocked")
		return
	}
	if err := a.store.Delete(r.PathValue("id")); err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "deleted": r.PathValue("id")})
}
