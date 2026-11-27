package main

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"image/png"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestPublicHomeAndDocument(t *testing.T) {
	app, store := newTestApp(t)
	doc := addTestDoc(t, store, "Alpha report", "# Alpha\n\nunique body term")

	rr := doReq(app, httptest.NewRequest("GET", "http://localhost/", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("home status: %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{`id="searchInput"`, `class="card card-og"`, `/card/` + doc.ID, `application/ld+json`} {
		if !strings.Contains(body, want) {
			t.Errorf("home missing %q", want)
		}
	}
	for _, unwanted := range []string{`class="hero`, `All documents`, `card-og-cta`, `class="brand-name"`, `class="main-nav"`, `href="/sitemap.xml"`, `href="/healthz"`} {
		if strings.Contains(body, unwanted) {
			t.Errorf("home still has %q", unwanted)
		}
	}
	if !strings.Contains(body, `content="https://public.test/og/`+doc.ID) {
		t.Error("home og:image does not use latest document")
	}

	rr = doReq(app, httptest.NewRequest("GET", "http://localhost/d/"+doc.ID, nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("document status: %d", rr.Code)
	}
	docHTML := rr.Body.String()
	if !strings.Contains(docHTML, `lang="en"`) || !strings.Contains(docHTML, `unique body term`) {
		t.Error("document content or lang missing")
	}
	if strings.Contains(docHTML, `href="/sitemap.xml"`) || strings.Contains(docHTML, `href="/healthz"`) {
		t.Error("document footer should match the home footer (RSS + copyright only)")
	}
	if !strings.Contains(docHTML, `https://public.test/og/`+doc.ID) {
		t.Error("document og:image missing")
	}

	rr = doReq(app, httptest.NewRequest("GET", "http://localhost/raw/"+doc.ID, nil))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "unique body term") {
		t.Errorf("raw: %d %q", rr.Code, rr.Body.String())
	}
	rr = doReq(app, httptest.NewRequest("GET", "http://localhost/download/"+doc.ID, nil))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Header().Get("Content-Disposition"), "attachment") {
		t.Errorf("download: %d %v", rr.Code, rr.Header())
	}
}

func TestSocialImagesAndFeed(t *testing.T) {
	app, store := newTestApp(t)
	doc := addTestDoc(t, store, "Image report", "body")

	rr := doReq(app, httptest.NewRequest("GET", "http://localhost/og/"+doc.ID+".png", nil))
	if rr.Code != http.StatusOK || rr.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("og: %d %s", rr.Code, rr.Header().Get("Content-Type"))
	}
	if c, err := png.DecodeConfig(bytes.NewReader(rr.Body.Bytes())); err != nil || c.Width != 1200 || c.Height != 630 {
		t.Fatalf("og dimensions: %+v %v", c, err)
	}

	rr = doReq(app, httptest.NewRequest("GET", "http://localhost/card/"+doc.ID+".png?theme=dark", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("card: %d", rr.Code)
	}
	if c, err := png.DecodeConfig(bytes.NewReader(rr.Body.Bytes())); err != nil || c.Width != 1200 || c.Height != 800 {
		t.Fatalf("card dimensions: %+v %v", c, err)
	}

	rr = doReq(app, httptest.NewRequest("GET", "http://localhost/feed.xml", nil))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Header().Get("Content-Type"), "application/rss+xml") {
		t.Fatalf("feed: %d %s", rr.Code, rr.Header().Get("Content-Type"))
	}
	var feed struct {
		Channel struct {
			Title string `xml:"title"`
			Items []struct {
				Title string `xml:"title"`
			} `xml:"item"`
		} `xml:"channel"`
	}
	if err := xml.Unmarshal(rr.Body.Bytes(), &feed); err != nil {
		t.Fatalf("feed xml: %v", err)
	}
	if feed.Channel.Title == "" || len(feed.Channel.Items) != 1 {
		t.Fatalf("feed content: %+v", feed)
	}

	rr = doReq(app, httptest.NewRequest("GET", "http://localhost/sitemap.xml", nil))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "/d/"+doc.ID) {
		t.Errorf("sitemap: %d", rr.Code)
	}
	rr = doReq(app, httptest.NewRequest("GET", "http://localhost/robots.txt", nil))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "Sitemap:") {
		t.Errorf("robots: %d", rr.Code)
	}
}

func TestAPIDocsPage(t *testing.T) {
	app, _ := newTestApp(t)
	rr := doReq(app, httptest.NewRequest("GET", "http://localhost/api", nil))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "/api/upload") {
		t.Fatalf("api docs: %d", rr.Code)
	}
	rr = doReq(app, httptest.NewRequest("GET", "http://localhost/missing", nil))
	if rr.Code != http.StatusNotFound || !strings.Contains(rr.Body.String(), `href="/api"`) || strings.Contains(rr.Body.String(), `href="/#api"`) {
		t.Fatalf("404 api link: %d", rr.Code)
	}
}

func TestStudioAuthAndCSRF(t *testing.T) {
	app, _ := newTestApp(t)
	rr := doReq(app, httptest.NewRequest("GET", "http://localhost/studio", nil))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `action="/studio"`) {
		t.Fatalf("login page: %d", rr.Code)
	}
	rr = doReq(app, httptest.NewRequest("GET", "http://localhost/studio/admin", nil))
	if rr.Code != http.StatusSeeOther || !strings.HasPrefix(rr.Header().Get("Location"), "/studio") {
		t.Fatalf("admin redirect: %d %s", rr.Code, rr.Header().Get("Location"))
	}

	bad := doReq(app, formRequest("POST", "/studio", map[string][]string{"key": {"wrong"}}))
	if bad.Code != http.StatusUnauthorized {
		t.Fatalf("bad login: %d", bad.Code)
	}
	good := doReq(app, formRequest("POST", "/studio", map[string][]string{"key": {"test-key"}}))
	if good.Code != http.StatusSeeOther {
		t.Fatalf("good login: %d", good.Code)
	}
	cookies := good.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("no session cookie")
	}
	req := httptest.NewRequest("GET", "http://localhost/studio/admin", nil)
	req.AddCookie(cookies[0])
	admin := doReq(app, req)
	if admin.Code != http.StatusOK || !strings.Contains(admin.Body.String(), "Studio") {
		t.Fatalf("admin: %d", admin.Code)
	}

	cross := httptest.NewRequest("POST", "http://localhost/studio/logout", strings.NewReader(""))
	cross.Header.Set("Origin", "https://evil.example")
	if got := doReq(app, cross).Code; got != http.StatusForbidden {
		t.Fatalf("cross-site logout: %d", got)
	}

	for _, path := range []string{"/login", "/upload", "/admin"} {
		if got := doReq(app, httptest.NewRequest("GET", "http://localhost"+path, nil)).Code; got != http.StatusNotFound {
			t.Errorf("legacy %s: %d", path, got)
		}
	}
}

func TestAPILifecycle(t *testing.T) {
	app, _ := newTestApp(t)
	noAuth, ct := multipartUpload(t, "API report", "api body")
	req := httptest.NewRequest("POST", "http://localhost/api/upload", noAuth)
	req.Header.Set("Content-Type", ct)
	if got := doReq(app, req).Code; got != http.StatusUnauthorized {
		t.Fatalf("unauthenticated upload: %d", got)
	}

	body, ct := multipartUpload(t, "API report", "api body")
	req = httptest.NewRequest("POST", "http://localhost/api/upload", body)
	req.Header.Set("Content-Type", ct)
	req.Header.Set("X-API-Key", "test-key")
	rr := doReq(app, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("upload: %d %s", rr.Code, rr.Body.String())
	}
	resp := decodeJSON(t, rr)
	docMap, _ := resp["document"].(map[string]any)
	id, _ := docMap["id"].(string)
	if id == "" {
		t.Fatalf("no id: %s", rr.Body.String())
	}

	rr = doReq(app, httptest.NewRequest("GET", "http://localhost/api/documents", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("list: %d", rr.Code)
	}
	rr = doReq(app, httptest.NewRequest("GET", "http://localhost/api/documents/"+id, nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("get: %d", rr.Code)
	}

	body, ct = multipartUpload(t, "API report", "replacement body")
	req = httptest.NewRequest("PUT", "http://localhost/api/documents/"+id, body)
	req.Header.Set("Content-Type", ct)
	req.Header.Set("X-API-Key", "test-key")
	rr = doReq(app, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("replace: %d %s", rr.Code, rr.Body.String())
	}
	rr = doReq(app, httptest.NewRequest("GET", "http://localhost/api/documents/"+id+"/versions", nil))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"versions"`) {
		t.Fatalf("versions: %d", rr.Code)
	}

	req = httptest.NewRequest("DELETE", "http://localhost/api/documents/"+id, nil)
	req.Header.Set("X-API-Key", "test-key")
	rr = doReq(app, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("delete: %d", rr.Code)
	}
	if got := doReq(app, httptest.NewRequest("GET", "http://localhost/api/documents/"+id, nil)).Code; got != http.StatusNotFound {
		t.Fatalf("deleted doc still available: %d", got)
	}
}

func TestSecurityHeadersAndJSONLD(t *testing.T) {
	app, store := newTestApp(t)
	doc := addTestDoc(t, store, "Meta report", "body")
	rr := doReq(app, httptest.NewRequest("GET", "http://localhost/d/"+doc.ID, nil))
	csp := rr.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "script-src 'self' 'nonce-") || strings.Contains(csp, "script-src 'self' 'unsafe-inline'") {
		t.Fatalf("csp: %s", csp)
	}
	if rr.Header().Get("Strict-Transport-Security") != "" {
		t.Fatal("HSTS should only be set on HTTPS")
	}
	body := rr.Body.String()
	m := regexp.MustCompile(`<script type="application/ld\+json" nonce="([^"]+)">(.*?)</script>`).FindStringSubmatch(body)
	if len(m) != 3 {
		t.Fatalf("jsonld missing: %s", body)
	}
	var ld map[string]any
	if err := json.Unmarshal([]byte(m[2]), &ld); err != nil {
		t.Fatalf("jsonld json: %v", err)
	}
	if ld["@type"] != "DigitalDocument" {
		t.Fatalf("jsonld type: %v", ld["@type"])
	}
	if !strings.Contains(csp, "nonce-"+m[1]) {
		t.Fatal("jsonld nonce does not match CSP")
	}

	httpsReq := httptest.NewRequest("GET", "http://public.test/", nil)
	httpsReq.Host = "public.test"
	httpsReq.Header.Set("X-Forwarded-Proto", "https")
	httpsRR := doReq(app, httpsReq)
	if httpsRR.Code != http.StatusOK || httpsRR.Header().Get("Strict-Transport-Security") == "" {
		t.Fatalf("https headers: %d %v", httpsRR.Code, httpsRR.Header())
	}

	httpReq := httptest.NewRequest("GET", "http://public.test/", nil)
	httpReq.Host = "public.test"
	if got := doReq(app, httpReq).Code; got != http.StatusPermanentRedirect {
		t.Fatalf("http redirect: %d", got)
	}
}

func TestTagSearch(t *testing.T) {
	app, store := newTestApp(t)
	tagged, err := store.Create(strings.NewReader("tagged body"), "tagged.md", "Tagged", "", []string{"security"}, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.Create(strings.NewReader("other body"), "other.md", "Other", "", []string{"misc"}, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	rr := doReq(app, httptest.NewRequest("GET", "http://localhost/?tag=security", nil))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), tagged.Title) || strings.Contains(rr.Body.String(), other.Title) {
		t.Fatalf("tag search: %d", rr.Code)
	}
	rr = doReq(app, httptest.NewRequest("GET", "http://localhost/d/"+tagged.ID, nil))
	if !strings.Contains(rr.Body.String(), `/?tag=security`) {
		t.Fatal("view tag link missing")
	}
}

func TestSearchAndHealth(t *testing.T) {
	app, store := newTestApp(t)
	addTestDoc(t, store, "Unrelated", "nothing here")
	addTestDoc(t, store, "Search target", "needle in the body")
	rr := doReq(app, httptest.NewRequest("GET", "http://localhost/?q=needle", nil))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "Search target") {
		t.Fatalf("search: %d", rr.Code)
	}
	if strings.Contains(rr.Body.String(), "Unrelated") {
		t.Fatal("search included non-matching doc")
	}
	rr = doReq(app, httptest.NewRequest("GET", "http://localhost/healthz", nil))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"ok":true`) {
		t.Fatalf("health: %d %s", rr.Code, rr.Body.String())
	}
	b, err := embedded.ReadFile("static/app.js")
	if err != nil || !strings.Contains(string(b), "searchInput.focus") {
		t.Fatal("F shortcut missing")
	}
}
