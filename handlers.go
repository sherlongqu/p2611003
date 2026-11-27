package main

import (
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const pageSize = 9

type baseData struct {
	SiteName        string
	Tagline         string
	Title           string
	MetaDescription string
	Section         string
	IsAdmin         bool
	Year            int
	BaseURL         string
	Canonical       string
	Robots          string
	OGType          string
	OGTitle         string
	OGImage         string
	Nonce           string
	JSONLD          template.JS
}

type homeData struct {
	baseData
	Docs      []Document
	Q         string
	Page      int
	Pages     int
	PrevURL   string
	NextURL   string
	Total     int
	Filtering bool
}

type viewData struct {
	baseData
	Doc         Document
	Rendered    template.HTML
	TOC         []Heading
	ReadingTime int
	ShareURL    string
	RawURL      string
	DownloadURL string
	KindLabel   string
	DocLang     string
	HasMermaid  bool
}

type uploadData struct {
	baseData
	MaxMB int64
	Error string
}

type adminData struct {
	baseData
	Docs    []Document
	Stats   Stats
	KeyHint string
	APIBase string
}

type loginData struct {
	baseData
	Error string
	Next  string
}

type apiDocument struct {
	Document
	URL string `json:"url"`
}

func (a *App) baseData(r *http.Request, section string) baseData {
	baseURL := a.publicBaseURL(r)
	return baseData{
		SiteName:        a.cfg.SiteName,
		Tagline:         a.cfg.SiteTagline,
		Title:           a.cfg.SiteName + " · " + a.cfg.SiteTagline,
		MetaDescription: "Public reports from ArchHub: PDF, HTML, Markdown and plain text. Published once, readable by anyone.",
		Section:         section,
		IsAdmin:         a.isAdmin(r),
		Year:            time.Now().Year(),
		BaseURL:         baseURL,
		Canonical:       baseURL + r.URL.Path,
		OGType:          "website",
		OGTitle:         a.cfg.SiteName,
		OGImage:         baseURL + asset("og.png"),
		Nonce:           nonceFromRequest(r),
	}
}

// privateBase marks the sign-in and console pages as non-indexable.
func (a *App) privateBase(r *http.Request, section string) baseData {
	base := a.baseData(r, section)
	base.Canonical = ""
	base.Robots = "noindex, nofollow"
	return base
}

func (a *App) handleHome(w http.ResponseWriter, r *http.Request) {
	q := limitRunes(strings.TrimSpace(r.URL.Query().Get("q")), 80)
	if tag := limitRunes(strings.TrimSpace(r.URL.Query().Get("tag")), 40); tag != "" {
		q = "#" + tag
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}

	docs := a.store.List(q, "", "new")
	total := len(docs)
	pages := (total + pageSize - 1) / pageSize
	if pages < 1 {
		pages = 1
	}
	if page > pages {
		page = pages
	}
	start := (page - 1) * pageSize
	end := start + pageSize
	if end > total {
		end = total
	}
	items := docs[start:end]
	filtering := q != ""

	base := a.baseData(r, "home")
	base.Canonical = a.publicBaseURL(r) + "/"
	if filtering || page > 1 {
		// Search and paged result states are not the bare home page; keep the
		// utility footer links visible there.
		base.Section = "home-search"
	}
	if filtering {
		base.Title = "Search “" + q + "” · " + a.cfg.SiteName
		base.Robots = "noindex, follow"
	}
	if page > 1 {
		base.Robots = "noindex, follow"
	}
	base.JSONLD = a.homeJSONLD(base)
	if page == 1 && !filtering && len(items) > 0 {
		// The home preview is the latest report, not a branded logo card.
		base.OGImage = fmt.Sprintf("%s/og/%s.png?v=%d", base.BaseURL, items[0].ID, items[0].UploadedAt.Unix())
	}
	data := homeData{
		baseData:  base,
		Docs:      items,
		Q:         q,
		Page:      page,
		Pages:     pages,
		Total:     total,
		Filtering: filtering,
	}
	if page > 1 {
		data.PrevURL = pageLink(r, page-1)
	}
	if page < pages {
		data.NextURL = pageLink(r, page+1)
	}
	a.render(w, http.StatusOK, "index", data)
}

func pageLink(r *http.Request, page int) string {
	v := url.Values{}
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		v.Set("q", q)
	}
	if page > 1 {
		v.Set("page", strconv.Itoa(page))
	}
	if len(v) == 0 {
		return "/"
	}
	return "/?" + v.Encode()
}

func (a *App) handleView(w http.ResponseWriter, r *http.Request) {
	doc, ok := a.store.Get(r.PathValue("id"))
	if !ok {
		a.notFound(w, r)
		return
	}

	base := a.baseData(r, "")
	base.Title = doc.Title + " · " + a.cfg.SiteName
	base.Canonical = a.publicBaseURL(r) + "/d/" + doc.ID
	base.OGType = "article"
	base.OGTitle = doc.Title
	base.OGImage = fmt.Sprintf("%s/og/%s.png?v=%d", base.BaseURL, doc.ID, doc.UploadedAt.Unix())
	docLang := detectLang(doc.Title + " " + doc.Description + " " + doc.Excerpt)
	base.JSONLD = a.docJSONLD(base, doc, docLang)
	if doc.Description != "" {
		base.MetaDescription = doc.Description
	} else if doc.Excerpt != "" {
		base.MetaDescription = doc.Excerpt
	}

	var (
		rendered    template.HTML
		toc         []Heading
		readingTime int
	)
	switch doc.Type {
	case "md":
		if src, err := os.ReadFile(a.store.FilePath(doc)); err == nil {
			rendered = renderMarkdown(src)
			toc = buildTOC([]byte(rendered))
			readingTime = readingMinutes(src)
		}
	case "txt":
		if src, err := os.ReadFile(a.store.FilePath(doc)); err == nil {
			rendered = plainTextHTML(src)
			readingTime = readingMinutes(src)
		}
	}

	data := viewData{
		baseData:    base,
		Doc:         doc,
		Rendered:    rendered,
		TOC:         toc,
		ReadingTime: readingTime,
		HasMermaid:  hasMermaid(rendered),
		ShareURL:    fmt.Sprintf("%s/d/%s?v=%d", base.BaseURL, doc.ID, doc.UploadedAt.Unix()),
		RawURL:      "/raw/" + doc.ID,
		DownloadURL: "/download/" + doc.ID,
		KindLabel:   typeLabel(doc.Type),
		DocLang:     docLang,
	}
	a.render(w, http.StatusOK, "view", data)
}

func (a *App) handleRaw(w http.ResponseWriter, r *http.Request) {
	doc, ok := a.store.Get(r.PathValue("id"))
	if !ok {
		a.notFound(w, r)
		return
	}
	f, err := a.store.Open(doc)
	if err != nil {
		a.notFound(w, r)
		return
	}
	defer f.Close()

	h := w.Header()
	h.Set("Content-Type", mimeFor(doc))
	h.Set("Content-Disposition", contentDisposition("inline", doc))
	h.Set("Cache-Control", "public, max-age=300")
	if doc.Type == "html" || (doc.Type == "image" && doc.Ext == ".svg") {
		h.Set("Content-Security-Policy", "sandbox allow-scripts; default-src 'none'; script-src 'unsafe-inline'; img-src * data: blob:; media-src *; style-src 'unsafe-inline'; font-src * data:;")
	}
	http.ServeContent(w, r, doc.Filename, doc.UploadedAt, f)
}

func (a *App) handleDownload(w http.ResponseWriter, r *http.Request) {
	doc, ok := a.store.Get(r.PathValue("id"))
	if !ok {
		a.notFound(w, r)
		return
	}
	f, err := a.store.Open(doc)
	if err != nil {
		a.notFound(w, r)
		return
	}
	defer f.Close()

	h := w.Header()
	h.Set("Content-Type", "application/octet-stream")
	h.Set("Content-Disposition", contentDisposition("attachment", doc))
	h.Set("Cache-Control", "private, max-age=0")
	http.ServeContent(w, r, doc.Filename, doc.UploadedAt, f)
}

func contentDisposition(kind string, doc Document) string {
	ascii := "document" + doc.Ext
	return fmt.Sprintf("%s; filename=%q; filename*=UTF-8''%s", kind, ascii, url.PathEscape(doc.Filename))
}

// readUpload accepts both multipart forms and raw binary streams.
func (a *App) handleVersionDownload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	version, f, ok := a.store.OpenVersion(id, r.PathValue("versionID"))
	if !ok {
		a.notFound(w, r)
		return
	}
	defer f.Close()
	doc := Document{Filename: version.Filename, Ext: version.Ext, Type: version.Type}
	h := w.Header()
	h.Set("Content-Type", mimeFor(doc))
	h.Set("Content-Disposition", contentDisposition("attachment", doc))
	h.Set("Cache-Control", "private, max-age=0")
	http.ServeContent(w, r, version.Filename, version.SavedAt, f)
}

func (a *App) notFound(w http.ResponseWriter, r *http.Request) {
	base := a.privateBase(r, "")
	base.Title = "Page not found · " + a.cfg.SiteName
	a.render(w, http.StatusNotFound, "notfound", base)
}
