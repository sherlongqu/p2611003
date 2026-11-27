package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"syscall"
	"time"

	"golang.org/x/net/html"
)

const (
	maxInlineResourceBytes = 2 << 20
	maxInlineTotalBytes    = 6 << 20
	maxInlineResources     = 20
	maxInlineDocumentBytes = 8 << 20
	maxInlineCSSDepth      = 3
	inlineFetchBudget      = 30 * time.Second
)

var (
	errBlockedInlineHost = errors.New("inline host is not public")
	inlineCSSImportRe    = regexp.MustCompile(`(?is)@import\s+(?:url\(\s*)?['"]?([^'")\s;]+)['"]?\s*\)?\s*[^;]*;`)
	inlineCSSURLRe       = regexp.MustCompile(`url\(\s*['"]?([^'")]+)['"]?\s*\)`)
)

// inlineResource is one external reference found in an uploaded HTML document.
type inlineResource struct {
	URL    string `json:"url"`
	Kind   string `json:"kind"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
	Bytes  int64  `json:"bytes,omitempty"`
}

// inlineReport summarises what happened while making a document self-contained.
type inlineReport struct {
	Inlined   int              `json:"inlined"`
	Failed    int              `json:"failed"`
	Remaining int              `json:"remaining"`
	Resources []inlineResource `json:"resources,omitempty"`
}

func (r *inlineReport) empty() bool {
	return r == nil || (r.Inlined == 0 && r.Failed == 0 && r.Remaining == 0)
}

// inlineFetcher is a variable so tests can serve resources without the network.
var inlineFetcher = fetchInlineResource

var inlineDialer = &net.Dialer{
	Timeout: 5 * time.Second,
	Control: func(network, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		ip := net.ParseIP(host)
		if ip == nil || !isPublicIP(ip) {
			return errBlockedInlineHost
		}
		return nil
	},
}

var inlineClient = &http.Client{
	Timeout: 8 * time.Second,
	Transport: &http.Transport{
		Proxy:               nil,
		DialContext:         inlineDialer.DialContext,
		TLSHandshakeTimeout: 5 * time.Second,
		MaxIdleConns:        4,
		IdleConnTimeout:     15 * time.Second,
	},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		return validateInlineURL(req.URL)
	},
}

func validateInlineURL(u *url.URL) error {
	if u == nil || u.Host == "" || u.User != nil {
		return errors.New("invalid URL")
	}
	if u.Scheme != "https" {
		return errors.New("only https is allowed")
	}
	if port := u.Port(); port != "" && port != "443" {
		return errors.New("port not allowed")
	}
	return nil
}

func fetchInlineResource(ctx context.Context, raw string) ([]byte, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, err
	}
	if err := validateInlineURL(u); err != nil {
		return nil, err
	}
	reqCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "ReportForPublic/1.0 (+html inliner)")
	resp, err := inlineClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxInlineResourceBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxInlineResourceBytes {
		return nil, errors.New("resource too large")
	}
	return data, nil
}

// inlineHTMLDocument inlines external stylesheets and scripts into src and
// reports what it could not resolve. The document is returned unchanged when
// it is empty, too large, or cannot be parsed.
func (a *App) inlineHTMLDocument(src []byte) ([]byte, inlineReport) {
	var rep inlineReport
	if len(src) == 0 || len(src) > maxInlineDocumentBytes {
		return src, rep
	}
	doc, err := html.Parse(bytes.NewReader(src))
	if err != nil {
		return src, rep
	}

	ctx, cancel := context.WithTimeout(context.Background(), inlineFetchBudget)
	defer cancel()
	st := &inlineState{ctx: ctx, budget: maxInlineTotalBytes}

	st.walk(doc)

	var buf bytes.Buffer
	if err := html.Render(&buf, doc); err != nil {
		return src, rep
	}
	return buf.Bytes(), st.report
}

type inlineState struct {
	ctx    context.Context
	budget int64
	count  int
	report inlineReport
}

func (st *inlineState) walk(n *html.Node) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		if c.Type == html.ElementNode {
			switch strings.ToLower(c.Data) {
			case "link":
				st.handleLink(c)
				c = next
				continue
			case "script":
				st.handleScript(c)
			case "style":
				st.handleStyle(c)
			}
		}
		st.walk(c)
		c = next
	}
}

func (st *inlineState) handleLink(n *html.Node) {
	rel := strings.ToLower(nodeAttr(n, "rel"))
	href := strings.TrimSpace(nodeAttr(n, "href"))

	if strings.Contains(rel, "preconnect") || strings.Contains(rel, "dns-prefetch") ||
		strings.Contains(rel, "prefetch") || strings.Contains(rel, "preload") {
		removeNode(n)
		return
	}
	if !strings.Contains(rel, "stylesheet") || href == "" {
		return
	}

	abs, ok := absoluteHTTPS(href)
	if !ok {
		st.report.Remaining++
		return
	}
	if st.count >= maxInlineResources {
		st.report.Remaining++
		st.report.Resources = append(st.report.Resources, inlineResource{URL: abs, Kind: "stylesheet", Status: "skipped", Error: "resource limit reached"})
		return
	}
	data, err := st.fetch(abs)
	if err != nil {
		st.report.Failed++
		st.report.Remaining++
		st.report.Resources = append(st.report.Resources, inlineResource{URL: abs, Kind: "stylesheet", Status: "failed", Error: err.Error()})
		return
	}

	st.count++
	st.report.Inlined++
	st.report.Resources = append(st.report.Resources, inlineResource{URL: abs, Kind: "stylesheet", Status: "inlined", Bytes: int64(len(data))})

	base, _ := url.Parse(abs)
	css := st.processCSS(string(data), base, 0)
	replaceNode(n, textElement("style", css))
}

func (st *inlineState) handleScript(n *html.Node) {
	src := strings.TrimSpace(nodeAttr(n, "src"))
	if src == "" {
		return
	}
	abs, ok := absoluteHTTPS(src)
	if !ok {
		st.report.Remaining++
		return
	}
	if st.count >= maxInlineResources {
		st.report.Remaining++
		st.report.Resources = append(st.report.Resources, inlineResource{URL: abs, Kind: "script", Status: "skipped", Error: "resource limit reached"})
		return
	}
	data, err := st.fetch(abs)
	if err != nil {
		st.report.Failed++
		st.report.Remaining++
		st.report.Resources = append(st.report.Resources, inlineResource{URL: abs, Kind: "script", Status: "failed", Error: err.Error()})
		return
	}

	st.count++
	st.report.Inlined++
	st.report.Resources = append(st.report.Resources, inlineResource{URL: abs, Kind: "script", Status: "inlined", Bytes: int64(len(data))})

	n.Attr = dropAttrs(n.Attr, "src", "integrity", "crossorigin", "async", "defer", "referrerpolicy")
	for c := n.FirstChild; c != nil; c = n.FirstChild {
		n.RemoveChild(c)
	}
	n.AppendChild(&html.Node{Type: html.TextNode, Data: string(data)})
}

func (st *inlineState) handleStyle(n *html.Node) {
	if n.FirstChild == nil || n.FirstChild.Type != html.TextNode {
		return
	}
	css := n.FirstChild.Data
	out := st.processCSS(css, nil, 0)
	if out != css {
		n.FirstChild.Data = out
	}
}

func (st *inlineState) fetch(abs string) ([]byte, error) {
	data, err := inlineFetcher(st.ctx, abs)
	if err != nil {
		return nil, err
	}
	if st.budget-int64(len(data)) < 0 {
		return nil, errors.New("inline budget exceeded")
	}
	st.budget -= int64(len(data))
	return data, nil
}

func (st *inlineState) processCSS(css string, base *url.URL, depth int) string {
	if depth > maxInlineCSSDepth {
		return css
	}
	css = inlineCSSImportRe.ReplaceAllStringFunc(css, func(match string) string {
		sub := inlineCSSImportRe.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		abs, ok := resolveHTTPS(base, sub[1])
		if !ok {
			return match
		}
		if st.count >= maxInlineResources {
			return match
		}
		data, err := st.fetch(abs)
		if err != nil {
			st.report.Failed++
			st.report.Resources = append(st.report.Resources, inlineResource{URL: abs, Kind: "stylesheet", Status: "failed", Error: err.Error()})
			return match
		}
		st.count++
		st.report.Inlined++
		st.report.Resources = append(st.report.Resources, inlineResource{URL: abs, Kind: "stylesheet", Status: "inlined", Bytes: int64(len(data))})
		nested, _ := url.Parse(abs)
		return st.processCSS(string(data), nested, depth+1)
	})
	css = inlineCSSURLRe.ReplaceAllStringFunc(css, func(match string) string {
		sub := inlineCSSURLRe.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		abs, ok := resolveHTTPS(base, sub[1])
		if !ok {
			return match
		}
		return `url("` + abs + `")`
	})
	return css
}

func absoluteHTTPS(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil {
		return "", false
	}
	if u.Scheme != "https" {
		return "", false
	}
	if port := u.Port(); port != "" && port != "443" {
		return "", false
	}
	u.Fragment = ""
	return u.String(), true
}

func resolveHTTPS(base *url.URL, raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	lower := strings.ToLower(raw)
	if strings.HasPrefix(lower, "data:") || strings.HasPrefix(lower, "#") {
		return "", false
	}
	if strings.HasPrefix(lower, "https://") {
		return absoluteHTTPS(raw)
	}
	if strings.HasPrefix(lower, "http://") {
		return "", false
	}
	if base == nil {
		return "", false
	}
	ref, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	return absoluteHTTPS(base.ResolveReference(ref).String())
}

func nodeAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return a.Val
		}
	}
	return ""
}

func dropAttrs(attrs []html.Attribute, keys ...string) []html.Attribute {
	out := attrs[:0]
	for _, a := range attrs {
		drop := false
		for _, k := range keys {
			if strings.EqualFold(a.Key, k) {
				drop = true
				break
			}
		}
		if !drop {
			out = append(out, a)
		}
	}
	return out
}

func textElement(tag, text string) *html.Node {
	n := &html.Node{Type: html.ElementNode, Data: tag}
	n.AppendChild(&html.Node{Type: html.TextNode, Data: text})
	return n
}

func replaceNode(old, replacement *html.Node) {
	parent := old.Parent
	if parent == nil {
		return
	}
	parent.InsertBefore(replacement, old)
	parent.RemoveChild(old)
}

func removeNode(n *html.Node) {
	if n.Parent != nil {
		n.Parent.RemoveChild(n)
	}
}
