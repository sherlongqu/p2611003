package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func stubInlineFetcher(t *testing.T, resources map[string]string, fail map[string]bool) {
	t.Helper()
	orig := inlineFetcher
	t.Cleanup(func() { inlineFetcher = orig })
	inlineFetcher = func(_ context.Context, raw string) ([]byte, error) {
		if fail[raw] {
			return nil, errors.New("boom")
		}
		if body, ok := resources[raw]; ok {
			return []byte(body), nil
		}
		return nil, errors.New("unexpected url: " + raw)
	}
}

func TestInlineHTMLDocument(t *testing.T) {
	stubInlineFetcher(t, map[string]string{
		"https://cdn.example.com/app.css":   `@import "theme.css";body{background:url(./bg.png)}`,
		"https://cdn.example.com/theme.css": `:root{--x:1}`,
		"https://cdn.example.com/app.js":    `console.log("hi")`,
	}, nil)

	src := `<!doctype html><html><head>
<link rel="preconnect" href="https://cdn.example.com">
<link rel="stylesheet" href="https://cdn.example.com/app.css">
<script src="https://cdn.example.com/app.js" integrity="sha384-x" crossorigin="anonymous"></script>
</head><body><p>hi</p></body></html>`

	app := &App{}
	out, rep := app.inlineHTMLDocument([]byte(src))
	html := string(out)

	if rep.Inlined != 3 || rep.Failed != 0 || rep.Remaining != 0 {
		t.Fatalf("unexpected report: %+v", rep)
	}
	if strings.Contains(html, "rel=\"stylesheet\"") || strings.Contains(html, "src=\"https://cdn.example.com/app.js\"") {
		t.Fatalf("external reference survived: %s", html)
	}
	if strings.Contains(html, "preconnect") {
		t.Fatalf("preconnect not removed")
	}
	if !strings.Contains(html, "--x:1") || !strings.Contains(html, `url("https://cdn.example.com/bg.png")`) {
		t.Fatalf("css not inlined/rewritten: %s", html)
	}
	if !strings.Contains(html, `console.log("hi")`) {
		t.Fatalf("script not inlined: %s", html)
	}
}

func TestInlineHTMLDocumentKeepsFailedReferences(t *testing.T) {
	stubInlineFetcher(t, map[string]string{}, map[string]bool{"https://cdn.example.com/missing.css": true})

	src := `<html><head><link rel="stylesheet" href="https://cdn.example.com/missing.css"></head><body></body></html>`
	app := &App{}
	out, rep := app.inlineHTMLDocument([]byte(src))

	if rep.Failed != 1 || rep.Remaining != 1 || rep.Inlined != 0 {
		t.Fatalf("unexpected report: %+v", rep)
	}
	if !strings.Contains(string(out), "https://cdn.example.com/missing.css") {
		t.Fatalf("failed reference should be kept: %s", out)
	}
}

func TestInlineHTMLDocumentLeavesRelativeAndPlainHTTP(t *testing.T) {
	stubInlineFetcher(t, map[string]string{}, nil)

	src := `<html><head>
<link rel="stylesheet" href="/local.css">
<link rel="stylesheet" href="http://cdn.example.com/insecure.css">
</head><body></body></html>`
	app := &App{}
	out, rep := app.inlineHTMLDocument([]byte(src))

	if rep.Inlined != 0 || rep.Failed != 0 || rep.Remaining != 2 {
		t.Fatalf("unexpected report: %+v", rep)
	}
	if !strings.Contains(string(out), "/local.css") || !strings.Contains(string(out), "insecure.css") {
		t.Fatalf("references should be untouched: %s", out)
	}
}
