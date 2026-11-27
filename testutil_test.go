package main

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func newTestApp(t *testing.T) (*App, *Store) {
	t.Helper()
	store, err := NewStore(t.TempDir(), 20<<20)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	app, err := newApp(Config{
		APIKey:        "test-key",
		SessionSecret: "test-secret",
		SiteName:      "Test Public",
		SiteTagline:   "Test reports",
		PublicBaseURL: "https://public.test",
		MaxUploadMB:   5,
		MaxTotalMB:    20,
	}, store)
	if err != nil {
		t.Fatalf("newApp: %v", err)
	}
	return app, store
}

func addTestDoc(t *testing.T, store *Store, title, body string) Document {
	t.Helper()
	doc, err := store.Create(strings.NewReader(body), slugify(title)+".md", title, "", []string{"test"}, 5<<20)
	if err != nil {
		t.Fatalf("Create %q: %v", title, err)
	}
	return doc
}

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else if b.Len() > 0 && !strings.HasSuffix(b.String(), "-") {
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-") + "-" + time.Now().Format("150405")
}

func doReq(app *App, req *http.Request) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	app.handler(app.routes()).ServeHTTP(rr, req)
	return rr
}

func multipartUpload(t *testing.T, title, body string) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("file", slugify(title)+".md")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte(body))
	_ = mw.WriteField("title", title)
	_ = mw.Close()
	return &buf, mw.FormDataContentType()
}

func decodeJSON(t *testing.T, rr *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("json: %v body=%s", err, rr.Body.String())
	}
	return out
}

func formRequest(method, path string, values url.Values) *http.Request {
	req := httptest.NewRequest(method, "http://localhost"+path, strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://localhost")
	return req
}
