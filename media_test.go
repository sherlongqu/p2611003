package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/png"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{255, 0, 0, 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestRewriteMarkdownImages(t *testing.T) {
	local := map[string]string{
		"https://example.com/a.png": "/media/abc/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.png",
		"https://example.com/b.png": "/media/abc/bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb.png",
	}
	resolve := func(raw string) (string, bool) {
		v, ok := local[raw]
		return v, ok
	}
	src := "![Alt](https://example.com/a.png \"Title\")\n" +
		"<img src='https://example.com/b.png' alt=x>\n" +
		"[ref]: https://example.com/b.png\n" +
		"![Ref][ref]\n" +
		"![local](/already/local.png)\n" +
		"![data](data:image/png;base64,AAAA)\n" +
		"```\n![code](https://example.com/a.png)\n```\n"
	out := string(rewriteMarkdownImages([]byte(src), resolve))
	if !strings.Contains(out, `![Alt](/media/abc/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.png "Title")`) {
		t.Fatalf("inline not rewritten: %s", out)
	}
	if !strings.Contains(out, `src='/media/abc/bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb.png'`) {
		t.Fatalf("html not rewritten: %s", out)
	}
	if !strings.Contains(out, `[ref]: /media/abc/bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb.png`) {
		t.Fatalf("reference def not rewritten: %s", out)
	}
	if !strings.Contains(out, `![local](/already/local.png)`) || !strings.Contains(out, `data:image/png`) {
		t.Fatalf("local/data incorrectly changed: %s", out)
	}
	if !strings.Contains(out, `![code](https://example.com/a.png)`) {
		t.Fatalf("fenced code was changed: %s", out)
	}
}

func TestValidateImageURLAndIPs(t *testing.T) {
	for _, raw := range []string{
		"ftp://example.com/x.png",
		"http://example.com:8080/x.png",
		"http://user:pass@example.com/x.png",
	} {
		if _, err := validateImageURL(raw); err == nil {
			t.Errorf("expected reject: %s", raw)
		}
	}
	if _, err := validateImageURL("https://example.com/image.png"); err != nil {
		t.Errorf("public URL rejected: %v", err)
	}
	for _, raw := range []string{"127.0.0.1", "10.0.0.1", "192.168.1.1", "169.254.1.1", "100.64.0.1", "::1", "fc00::1", "0.0.0.0"} {
		ip := net.ParseIP(raw)
		if ip == nil {
			t.Fatalf("bad test IP %s", raw)
		}
		if isPublicIP(ip) {
			t.Errorf("private IP accepted: %s", raw)
		}
	}
	if ip := net.ParseIP("8.8.8.8"); ip == nil || !isPublicIP(ip) {
		t.Error("public IP rejected")
	}
}

func TestStoreLocalizesMarkdownImages(t *testing.T) {
	store, err := NewStore(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	store.fetchImage = func(context.Context, string) ([]byte, string, error) {
		return testPNG(t), ".png", nil
	}
	doc, err := store.Create(strings.NewReader("![Alt](https://example.com/a.png)\n"), "images.md", "Images", "", nil, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(store.FilePath(doc))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "/media/"+doc.ID+"/") || strings.Contains(string(src), "example.com") {
		t.Fatalf("source not localized: %s", src)
	}
	if rendered := string(renderMarkdown(src)); !strings.Contains(rendered, `/media/`+doc.ID+`/`) {
		t.Fatalf("rendered markdown lost media link: %s", rendered)
	}
	entries, err := os.ReadDir(filepath.Join(store.mediaDir, doc.ID))
	if err != nil || len(entries) != 1 {
		t.Fatalf("media entries: %v %v", entries, err)
	}
	f, err := store.OpenMedia(doc.ID, entries[0].Name())
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if c, err := png.DecodeConfig(f); err != nil || c.Width != 2 {
		t.Fatalf("media decode: %+v %v", c, err)
	}
	if store.Stats().Bytes <= doc.Size {
		t.Fatal("media bytes not included in stats")
	}

	if err := store.Delete(doc.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.OpenMedia(doc.ID, entries[0].Name()); err == nil {
		t.Fatal("media survived document delete")
	}
}

func TestStoreLocalizesDataURL(t *testing.T) {
	store, err := NewStore(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString(testPNG(t))
	src := "![Inline](data:image/png;base64," + encoded + ")\n"
	doc, err := store.Create(strings.NewReader(src), "inline.md", "Inline", "", nil, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(store.FilePath(doc))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "/media/"+doc.ID+"/") || strings.Contains(string(b), "data:image") {
		t.Fatalf("data URL not localized: %s", b)
	}
}

func TestLocalizeExistingMarkdown(t *testing.T) {
	store, err := NewStore(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	store.fetchImage = func(context.Context, string) ([]byte, string, error) {
		return nil, "", errors.New("offline")
	}
	doc, err := store.Create(strings.NewReader("![Old](https://example.com/old.png)\n"), "old.md", "Old", "", nil, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	src, _ := os.ReadFile(store.FilePath(doc))
	if !strings.Contains(string(src), "https://example.com/old.png") {
		t.Fatalf("setup did not leave external image: %s", src)
	}
	store.fetchImage = func(context.Context, string) ([]byte, string, error) {
		return testPNG(t), ".png", nil
	}
	n, err := store.LocalizeExistingMarkdown()
	if err != nil || n != 1 {
		t.Fatalf("localize existing: n=%d err=%v", n, err)
	}
	src, _ = os.ReadFile(store.FilePath(doc))
	if !strings.Contains(string(src), "/media/"+doc.ID+"/") || strings.Contains(string(src), "example.com") {
		t.Fatalf("existing source not localized: %s", src)
	}
	updated, _ := store.Get(doc.ID)
	if updated.Size != int64(len(src)) {
		t.Fatalf("metadata size not refreshed: doc=%d file=%d", updated.Size, len(src))
	}
}

func TestMediaHandler(t *testing.T) {
	app, store := newTestApp(t)
	store.fetchImage = func(context.Context, string) ([]byte, string, error) {
		return testPNG(t), ".png", nil
	}
	doc, err := store.Create(strings.NewReader("![Alt](https://example.com/a.png)\n"), "images.md", "Images", "", nil, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(store.mediaDir, doc.ID))
	if err != nil || len(entries) != 1 {
		t.Fatalf("media entries: %v %v", entries, err)
	}
	path := "/media/" + doc.ID + "/" + entries[0].Name()
	rr := doReq(app, httptest.NewRequest("GET", "http://localhost"+path, nil))
	if rr.Code != http.StatusOK || rr.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("media response: %d %s", rr.Code, rr.Header().Get("Content-Type"))
	}
	rr = doReq(app, httptest.NewRequest("GET", "http://localhost/media/"+doc.ID+"/not-an-image.txt", nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("invalid media name response: %d", rr.Code)
	}
}
