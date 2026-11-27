package main

import (
	"encoding/json"
	"encoding/xml"
	"html/template"
	"io"
	"net/http"
	"strings"
	"time"
)

func jsonLD(v any) template.JS {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return template.JS(b)
}

func (a *App) homeJSONLD(base baseData) template.JS {
	return jsonLD(map[string]any{
		"@context":    "https://schema.org",
		"@type":       "WebSite",
		"name":        a.cfg.SiteName,
		"url":         base.BaseURL + "/",
		"description": base.MetaDescription,
		"inLanguage":  "en-US",
		"publisher": map[string]any{
			"@type": "Organization",
			"name":  "p2611003",
			"url":   "https://github.com/sherlongqu/p2611003",
		},
	})
}

func (a *App) docJSONLD(base baseData, doc Document, lang string) template.JS {
	docURL := base.BaseURL + "/d/" + doc.ID
	description := doc.Description
	if description == "" {
		description = doc.Excerpt
	}
	return jsonLD(map[string]any{
		"@context":         "https://schema.org",
		"@type":            "DigitalDocument",
		"identifier":       doc.ID,
		"name":             doc.Title,
		"headline":         doc.Title,
		"description":      description,
		"url":              docURL,
		"mainEntityOfPage": docURL,
		"datePublished":    doc.UploadedAt.Format(time.RFC3339),
		"dateModified":     doc.UploadedAt.Format(time.RFC3339),
		"inLanguage":       lang,
		"encodingFormat":   encodingFormatFor(doc),
		"contentSize":      doc.Size,
		"keywords":         strings.Join(doc.Tags, ", "),
		"isPartOf": map[string]any{
			"@type": "WebSite",
			"name":  a.cfg.SiteName,
			"url":   base.BaseURL + "/",
		},
		"publisher": map[string]any{
			"@type": "Organization",
			"name":  "p2611003",
			"url":   "https://github.com/sherlongqu/p2611003",
		},
	})
}

func (a *App) handleFeed(w http.ResponseWriter, r *http.Request) {
	type feedItem struct {
		Title       string   `xml:"title"`
		Link        string   `xml:"link"`
		GUID        string   `xml:"guid"`
		PubDate     string   `xml:"pubDate"`
		Description string   `xml:"description"`
		Categories  []string `xml:"category,omitempty"`
	}
	type feedChannel struct {
		Title         string     `xml:"title"`
		Link          string     `xml:"link"`
		Description   string     `xml:"description"`
		Language      string     `xml:"language"`
		LastBuildDate string     `xml:"lastBuildDate"`
		Generator     string     `xml:"generator"`
		TTL           int        `xml:"ttl"`
		Items         []feedItem `xml:"item"`
	}
	type feed struct {
		XMLName xml.Name    `xml:"rss"`
		Version string      `xml:"version,attr"`
		Channel feedChannel `xml:"channel"`
	}

	base := a.publicBaseURL(r)
	now := time.Now().UTC()
	f := feed{Version: "2.0"}
	f.Channel = feedChannel{
		Title:         a.cfg.SiteName,
		Link:          base + "/",
		Description:   "Public reports from ArchHub.",
		Language:      "en-US",
		LastBuildDate: now.Format(time.RFC1123Z),
		Generator:     a.cfg.SiteName,
		TTL:           60,
	}
	docs := a.store.List("", "", "new")
	if len(docs) > 50 {
		docs = docs[:50]
	}
	for _, d := range docs {
		desc := d.Description
		if desc == "" {
			desc = d.Excerpt
		}
		if desc == "" {
			desc = typeLabel(d.Type) + " document."
		}
		link := base + "/d/" + d.ID
		f.Channel.Items = append(f.Channel.Items, feedItem{
			Title:       d.Title,
			Link:        link,
			GUID:        link,
			PubDate:     d.UploadedAt.UTC().Format(time.RFC1123Z),
			Description: desc,
			Categories:  d.Tags,
		})
	}
	out, err := xml.MarshalIndent(f, "", "  ")
	if err != nil {
		http.Error(w, "feed error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = io.WriteString(w, xml.Header)
	_, _ = w.Write(out)
}

func (a *App) handleSitemap(w http.ResponseWriter, r *http.Request) {
	type urlEntry struct {
		Loc     string `xml:"loc"`
		LastMod string `xml:"lastmod,omitempty"`
	}
	type urlSet struct {
		XMLName xml.Name   `xml:"urlset"`
		Xmlns   string     `xml:"xmlns,attr"`
		URLs    []urlEntry `xml:"url"`
	}
	base := a.publicBaseURL(r)
	set := urlSet{Xmlns: "http://www.sitemaps.org/schemas/sitemap/0.9"}
	home := urlEntry{Loc: base + "/"}
	if latest := a.store.Stats().Latest; !latest.IsZero() {
		home.LastMod = latest.Format("2006-01-02")
	}
	set.URLs = append(set.URLs, home)
	for _, d := range a.store.List("", "", "new") {
		set.URLs = append(set.URLs, urlEntry{Loc: base + "/d/" + d.ID, LastMod: d.UploadedAt.Format("2006-01-02")})
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	_, _ = io.WriteString(w, xml.Header)
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	_ = enc.Encode(set)
}
