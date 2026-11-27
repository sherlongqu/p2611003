package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	stdhtml "html"
	"html/template"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	ghtml "github.com/yuin/goldmark/renderer/html"
)

var markdown = goldmark.New(
	goldmark.WithExtensions(
		extension.GFM,
		extension.Footnote,
		extension.Typographer,
	),
	goldmark.WithParserOptions(
		parser.WithAutoHeadingID(),
	),
	goldmark.WithRendererOptions(
		ghtml.WithXHTML(),
		ghtml.WithUnsafe(),
	),
)

// markdownSanitizer keeps the useful HTML that Markdown authors expect (tables,
// details blocks, task lists, footnotes) while stripping scripts, event handlers,
// iframes and style attributes. Raw HTML in a Markdown upload runs in the app
// origin, so it must never be trusted.
var markdownSanitizer = func() *bluemonday.Policy {
	p := bluemonday.UGCPolicy()
	// Heading IDs may contain non-Latin letters (Chinese, Japanese, Cyrillic,
	// Arabic, …). The anchor is an attribute value, not executable markup.
	idPattern := regexp.MustCompile(`^[\p{L}\p{N}_:-]+$`)
	classPattern := regexp.MustCompile(`^(language-[a-z0-9+#-]+|mermaid|footnotes|footnote-ref|footnote-backref|task-list|task-list-item|contains-task-list)$`)
	alignPattern := regexp.MustCompile(`(?i)^(left|right|center|justify)$`)

	p.AllowAttrs("id").Matching(idPattern).OnElements("h1", "h2", "h3", "h4", "h5", "h6", "a", "section", "li", "sup", "p", "div", "span")
	p.AllowAttrs("class").Matching(classPattern).OnElements("code", "pre", "section", "ul", "ol", "li", "a", "div", "span", "p")
	p.AllowAttrs("align").Matching(alignPattern).OnElements("p", "div", "table", "thead", "tbody", "tfoot", "tr", "th", "td", "h1", "h2", "h3", "h4", "h5", "h6")
	p.AllowAttrs("colspan", "rowspan").Matching(regexp.MustCompile(`^[0-9]{1,2}$`)).OnElements("td", "th")
	p.AllowAttrs("type", "checked", "disabled").OnElements("input")
	// GitHub-style READMEs often embed screenshots as base64 data URLs. Keep
	// validated raster data images (never SVG) so those documents still render.
	dataImagePrefix := regexp.MustCompile(`(?i)^image/(png|jpe?g|gif|webp|avif);base64,`)
	p.AllowURLSchemeWithCustomPolicy("data", func(u *url.URL) bool {
		if u.RawQuery != "" || u.Fragment != "" {
			return false
		}
		m := dataImagePrefix.FindString(u.Opaque)
		if m == "" {
			return false
		}
		_, err := base64.StdEncoding.DecodeString(u.Opaque[len(m):])
		return err == nil
	})
	p.AllowElements("details", "summary", "figure", "figcaption", "picture", "source", "kbd", "mark", "sup", "sub", "abbr", "time")
	p.AllowAttrs("open").OnElements("details")
	p.AllowAttrs("datetime").OnElements("time")
	return p
}()

// Heading is one entry of the generated table of contents.
type Heading struct {
	Level int
	ID    string
	Text  string
}

// Blocks without an info string still render as diagrams when they look like Mermaid.
var plainCodeRe = regexp.MustCompile(`(?s)<pre><code>(.*?)</code></pre>`)

var mermaidPrefixes = []string{
	"flowchart", "graph ", "sequencediagram", "classdiagram", "statediagram",
	"erdiagram", "gantt", "pie", "journey", "mindmap", "timeline",
	"quadrantchart", "gitgraph", "c4context", "sankey-beta", "xychart-beta", "block-beta",
}

func looksLikeMermaid(src string) bool {
	line := strings.TrimSpace(strings.SplitN(strings.TrimSpace(src), "\n", 2)[0])
	if line == "" {
		return false
	}
	lower := strings.ToLower(line)
	for _, prefix := range mermaidPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

var (
	leadingH1Re  = regexp.MustCompile(`(?s)^\s*<h1[^>]*>.*?</h1>\s*`)
	headingIDRe  = regexp.MustCompile(`(?s)<h([1-6])([^>]*)>(.*?)</h[1-6]>`)
	existingIDRe = regexp.MustCompile(`\s+id="[^"]*"`)
)

// assignHeadingIDs replaces Goldmark's ASCII-only auto IDs with stable
// multilingual slugs. Goldmark drops CJK characters, which produced anchors
// such as #1- and made multilingual headings hard to link to.
func assignHeadingIDs(html string) string {
	used := make(map[string]int)
	return headingIDRe.ReplaceAllStringFunc(html, func(block string) string {
		parts := headingIDRe.FindStringSubmatch(block)
		if len(parts) != 4 {
			return block
		}
		level, attrs, inner := parts[1], existingIDRe.ReplaceAllString(parts[2], ""), parts[3]
		text := stdhtml.UnescapeString(tagRe.ReplaceAllString(inner, ""))
		slug := headingSlug(text)
		if n := used[slug]; n > 0 {
			used[slug] = n + 1
			slug += "-" + strconv.Itoa(n+1)
		} else {
			used[slug] = 1
		}
		return "<h" + level + ` id="` + slug + `"` + attrs + ">" + inner + "</h" + level + ">"
	})
}

func headingSlug(text string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.TrimSpace(text) {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			b.WriteRune(unicode.ToLower(r))
			lastDash = false
			continue
		}
		if unicode.IsSpace(r) || unicode.IsPunct(r) {
			if b.Len() > 0 && !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		return "section"
	}
	return slug
}

var mermaidBlockRe = regexp.MustCompile(`(?s)<pre><code class="language-mermaid">(.*?)</code></pre>`)

func renderMarkdown(src []byte) template.HTML {
	var buf bytes.Buffer
	if err := markdown.Convert(src, &buf); err != nil {
		return template.HTML("<p>Could not render this Markdown document.</p>")
	}
	out := markdownSanitizer.Sanitize(buf.String())
	out = assignHeadingIDs(out)
	out = mermaidBlockRe.ReplaceAllString(out, `<pre class="mermaid">$1</pre>`)
	// The reading page already prints the document title, so a leading H1 is redundant.
	out = plainCodeRe.ReplaceAllStringFunc(out, func(block string) string {
		inner := block[len("<pre><code>") : len(block)-len("</code></pre>")]
		if looksLikeMermaid(stdhtml.UnescapeString(inner)) {
			return `<pre class="mermaid">` + inner + `</pre>`
		}
		return block
	})
	out = leadingH1Re.ReplaceAllString(out, "")
	return template.HTML(out)
}

func hasMermaid(rendered template.HTML) bool {
	return strings.Contains(string(rendered), `class="mermaid"`)
}

var (
	headingRe = regexp.MustCompile(`(?s)<h([23])\s+id="([^"]+)"[^>]*>(.*?)</h[23]>`)
	tagRe     = regexp.MustCompile(`(?s)<[^>]*>`)
	spaceRe   = regexp.MustCompile(`\s+`)
	mdImageRe = regexp.MustCompile(`!\[([^\]]*)\]\([^)]*\)`)
	mdRawRe   = regexp.MustCompile(`(?is)<(script|style)[^>]*>.*?</(script|style)>`)
	mdLinkRe  = regexp.MustCompile(`\[([^\]]+)\]\([^)]*\)`)
	mdFootRe  = regexp.MustCompile(`\[\^[^\]]*\]`)
	mdMarkRe  = regexp.MustCompile("[`*_~>#]+")
)

func buildTOC(rendered []byte) []Heading {
	matches := headingRe.FindAllSubmatch(rendered, -1)
	toc := make([]Heading, 0, len(matches))
	for _, m := range matches {
		level, _ := strconv.Atoi(string(m[1]))
		text := stdhtml.UnescapeString(strings.TrimSpace(tagRe.ReplaceAllString(string(m[3]), "")))
		if text == "" {
			continue
		}
		toc = append(toc, Heading{Level: level, ID: string(m[2]), Text: text})
	}
	return toc
}

func plainExcerpt(src []byte, limit int) string {
	if !utf8.Valid(src) {
		src = bytes.ToValidUTF8(src, []byte(" "))
	}
	s := string(src)
	s = mdRawRe.ReplaceAllString(s, " ")
	s = mdImageRe.ReplaceAllString(s, "$1")
	s = mdLinkRe.ReplaceAllString(s, "$1")
	s = mdFootRe.ReplaceAllString(s, "")
	s = mdMarkRe.ReplaceAllString(s, "")
	s = tagRe.ReplaceAllString(s, " ")
	s = stdhtml.UnescapeString(s)
	s = spaceRe.ReplaceAllString(strings.TrimSpace(s), " ")
	if limit <= 0 || utf8.RuneCountInString(s) <= limit {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:limit])) + "…"
}

func readingMinutes(src []byte) int {
	s := string(src)
	words := len(strings.Fields(s))
	cjk := 0
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			cjk++
		}
	}
	if words == 0 && cjk == 0 {
		return 0
	}
	minutes := float64(cjk)/400 + float64(words)/220
	if minutes < 1 {
		return 1
	}
	return int(math.Ceil(minutes))
}

// detectLang chooses a content language for the rendered article. The site UI
// stays en-US; this only tells assistive technology how to read the document.
func detectLang(s string) string {
	han, kana, latin := 0, 0, 0
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r):
			kana++
		case unicode.Is(unicode.Han, r):
			han++
		case unicode.IsLetter(r):
			latin++
		}
	}
	if kana > 0 {
		return "ja"
	}
	if han > 8 && han*4 > latin {
		return "zh"
	}
	return "en"
}

func typeLabel(t string) string {
	switch t {
	case "pdf":
		return "PDF"
	case "html":
		return "HTML"
	case "md":
		return "Markdown"
	case "txt":
		return "Plain text"
	case "image":
		return "Image"
	}
	return "File"
}

func typeShort(t string) string {
	switch t {
	case "pdf":
		return "PDF"
	case "html":
		return "HTML"
	case "md":
		return "MD"
	case "txt":
		return "TXT"
	case "image":
		return "IMG"
	}
	return "FILE"
}

func formatSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit && exp < 3; v /= unit {
		div *= unit
		exp++
	}
	val := float64(n) / float64(div)
	if val >= 100 {
		return fmt.Sprintf("%.0f %cB", val, "KMGTPE"[exp])
	}
	return fmt.Sprintf("%.1f %cB", val, "KMGTPE"[exp])
}

func formatDate(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.Local().Format("Jan 2, 2006")
}

func formatDateTime(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.Local().Format("Jan 2, 2006 · 3:04 PM")
}

func encodingFormatFor(d Document) string {
	switch d.Type {
	case "md":
		return "text/markdown"
	case "html":
		return "text/html"
	case "txt":
		return "text/plain"
	}
	return mimeFor(d)
}

func mimeFor(d Document) string {
	switch d.Type {
	case "pdf":
		return "application/pdf"
	case "html":
		return "text/html; charset=utf-8"
	case "md":
		return "text/plain; charset=utf-8"
	case "txt":
		return "text/plain; charset=utf-8"
	case "image":
		switch d.Ext {
		case ".svg":
			return "image/svg+xml"
		case ".png":
			return "image/png"
		case ".jpg", ".jpeg":
			return "image/jpeg"
		case ".webp":
			return "image/webp"
		case ".gif":
			return "image/gif"
		case ".avif":
			return "image/avif"
		}
	}
	return "application/octet-stream"
}

func plainTextHTML(src []byte) template.HTML {
	return template.HTML("<pre class=\"plaintext\">" + stdhtml.EscapeString(string(src)) + "</pre>")
}
