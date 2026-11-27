package main

import (
	"strings"
	"testing"
)

func TestRenderMarkdownSanitizesUntrustedHTML(t *testing.T) {
	src := []byte("## Heading\n\n<script>alert(1)</script>\n\n<img src=x onerror=alert(1)>\n\n| A | B |\n| - | - |\n| 1 | 2 |\n")
	out := string(renderMarkdown(src))
	if strings.Contains(out, "<script") || strings.Contains(strings.ToLower(out), "onerror") {
		t.Fatalf("unsafe markup survived: %s", out)
	}
	if !strings.Contains(out, "<table") {
		t.Fatalf("safe table markup was removed: %s", out)
	}
	if !strings.Contains(out, "<h2") {
		t.Fatalf("heading missing: %s", out)
	}
}

func TestRenderMarkdownMermaidAndTOC(t *testing.T) {
	src := []byte("## Section\n\n```mermaid\ngraph TD\n  A --> B\n```\n")
	out := renderMarkdown(src)
	if !strings.Contains(string(out), `class="mermaid"`) {
		t.Fatalf("mermaid block not converted: %s", out)
	}
	toc := buildTOC([]byte(out))
	if len(toc) != 1 || toc[0].Text != "Section" {
		t.Fatalf("bad toc: %+v", toc)
	}
}

func TestDetectLangAndReadingTime(t *testing.T) {
	if got := detectLang("这是一个中文标题，包含足够多的汉字用于检测语言"); got != "zh" {
		t.Fatalf("zh detect: %q", got)
	}
	if got := detectLang("This is an English document with enough letters."); got != "en" {
		t.Fatalf("en detect: %q", got)
	}
	if got := detectLang("これは日本語の文章です。"); got != "ja" {
		t.Fatalf("ja detect: %q", got)
	}
	if got := readingMinutes([]byte(strings.Repeat("中文", 400))); got < 1 {
		t.Fatalf("reading minutes: %d", got)
	}
}

func TestMultilingualHeadingAnchors(t *testing.T) {
	src := []byte("## 系统概述\n\n### Hello World\n\n## 系统概述\n")
	out := string(renderMarkdown(src))
	for _, want := range []string{`id="系统概述"`, `id="hello-world"`, `id="系统概述-2"`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing heading id %s in %s", want, out)
		}
	}
	toc := buildTOC([]byte(out))
	if len(toc) != 3 || toc[0].ID != "系统概述" || toc[1].ID != "hello-world" {
		t.Fatalf("bad multilingual toc: %+v", toc)
	}
}

func TestPlainTextHTML(t *testing.T) {
	out := string(plainTextHTML([]byte("<b>not html</b>")))
	if strings.Contains(out, "<b>") || !strings.Contains(out, "&lt;b&gt;") {
		t.Fatalf("plain text was not escaped: %s", out)
	}
}
