package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

const (
	maxRemoteImageBytes   = 8 << 20
	maxImagesPerMarkdown  = 32
	maxMarkdownMediaBytes = 32 << 20
	mediaFetchTimeout     = 20 * time.Second
)

var (
	mediaNameRe     = regexp.MustCompile(`^[a-f0-9]{32}\.(png|jpg|gif|webp|avif)$`)
	markdownImageRe = regexp.MustCompile(`!\[([^\]]*)\]\(\s*(?:<([^>\s]+)>|([^\s)]+))(\s+(?:"[^"]*"|'[^']*'|\([^)]*\)))?\s*\)`)
	htmlImgRe       = regexp.MustCompile(`(?i)(<img\b[^>]*\bsrc\s*=\s*)(?:"([^"]*)"|'([^']*)'|([^\s>]+))`)
	refDefRe        = regexp.MustCompile(`^([ \t]*\[[^\]\n]+\]:[ \t]*)(?:<([^>\s]+)>|(\S+))`)

	errBlockedImageHost = errors.New("image host is not public")
)

type imageFetcher func(context.Context, string) ([]byte, string, error)

func (s *Store) localizeMarkdownFile(path, docID string, budget int64) (int64, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	out, changed := s.localizeMarkdown(src, docID, budget)
	if !changed {
		return int64(len(src)), nil
	}
	if err := writeFileAtomic(path, out); err != nil {
		return 0, err
	}
	fi, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}

func (s *Store) localizeMarkdown(src []byte, docID string, budget int64) ([]byte, bool) {
	if budget <= 0 || (!strings.Contains(string(src), "![") && !strings.Contains(strings.ToLower(string(src)), "<img")) {
		return src, false
	}
	fetch := s.fetchImage
	if fetch == nil {
		fetch = fetchRemoteImage
	}
	cache := make(map[string]string)
	var added int64
	count := 0
	changed := false
	ctx, cancelAll := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancelAll()
	resolve := func(raw string) (string, bool) {
		raw = strings.TrimSpace(html.UnescapeString(raw))
		if local, ok := cache[raw]; ok {
			return local, true
		}
		if count >= maxImagesPerMarkdown {
			return "", false
		}
		var data []byte
		var ext string
		if isRemoteHTTP(raw) {
			fetchCtx, cancel := context.WithTimeout(ctx, mediaFetchTimeout)
			var err error
			data, ext, err = fetch(fetchCtx, raw)
			cancel()
			if err != nil {
				log.Printf("markdown image %s: %v", raw, err)
				return "", false
			}
		} else {
			var ok bool
			data, ext, ok = decodeDataImage(raw)
			if !ok {
				return "", false
			}
		}
		if added+int64(len(data)) > budget {
			return "", false
		}
		name, err := s.saveMedia(docID, data, ext)
		if err != nil {
			log.Printf("save media %s: %v", raw, err)
			return "", false
		}
		local := "/media/" + docID + "/" + name
		cache[raw] = local
		added += int64(len(data))
		count++
		changed = true
		return local, true
	}
	out := rewriteMarkdownImages(src, resolve)
	return out, changed
}

func (s *Store) saveMedia(docID string, data []byte, ext string) (string, error) {
	sum := sha256.Sum256(data)
	name := hex.EncodeToString(sum[:16]) + ext
	dir := filepath.Join(s.mediaDir, docID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	dest := filepath.Join(dir, name)
	if _, err := os.Stat(dest); err == nil {
		return name, nil
	}
	tmp, err := os.CreateTemp(dir, ".incoming-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmpName, dest); err != nil {
		return "", err
	}
	return name, nil
}

func rewriteMarkdownImages(src []byte, resolve func(string) (string, bool)) []byte {
	lines := strings.SplitAfter(string(src), "\n")
	var out strings.Builder
	inFence := false
	fence := ""
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "```") || strings.HasPrefix(trim, "~~~") {
			marker := trim[:3]
			if !inFence {
				inFence, fence = true, marker
			} else if marker == fence {
				inFence = false
			}
			out.WriteString(line)
			continue
		}
		if inFence {
			out.WriteString(line)
			continue
		}
		line = rewriteInlineMarkdownImages(line, resolve)
		line = rewriteHTMLImages(line, resolve)
		line = rewriteReferenceDefinition(line, resolve)
		out.WriteString(line)
	}
	return []byte(out.String())
}

func rewriteInlineMarkdownImages(line string, resolve func(string) (string, bool)) string {
	return markdownImageRe.ReplaceAllStringFunc(line, func(m string) string {
		parts := markdownImageRe.FindStringSubmatch(m)
		if len(parts) == 0 {
			return m
		}
		raw := parts[2]
		angle := raw != ""
		if raw == "" {
			raw = parts[3]
		}
		local, ok := resolve(raw)
		if !ok {
			return m
		}
		if angle {
			local = "<" + local + ">"
		}
		return "![" + parts[1] + "](" + local + parts[4] + ")"
	})
}

func rewriteHTMLImages(line string, resolve func(string) (string, bool)) string {
	return htmlImgRe.ReplaceAllStringFunc(line, func(m string) string {
		parts := htmlImgRe.FindStringSubmatch(m)
		if len(parts) == 0 {
			return m
		}
		raw := parts[2]
		quote := `"`
		if raw == "" {
			raw = parts[3]
			quote = `'`
		}
		if raw == "" {
			raw = parts[4]
			quote = ""
		}
		local, ok := resolve(raw)
		if !ok {
			return m
		}
		return parts[1] + quote + local + quote
	})
}

func rewriteReferenceDefinition(line string, resolve func(string) (string, bool)) string {
	parts := refDefRe.FindStringSubmatch(line)
	if len(parts) == 0 {
		return line
	}
	raw := parts[2]
	angle := raw != ""
	if raw == "" {
		raw = parts[3]
	}
	if !looksLikeImageURL(raw) {
		return line
	}
	local, ok := resolve(raw)
	if !ok {
		return line
	}
	if angle {
		local = "<" + local + ">"
	}
	return parts[1] + local + line[len(parts[0]):]
}

func looksLikeImageURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	switch strings.ToLower(filepath.Ext(u.Path)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".avif":
		return true
	}
	return false
}

func decodeDataImage(raw string) ([]byte, string, bool) {
	const prefix = "data:image/"
	lower := strings.ToLower(raw)
	if !strings.HasPrefix(lower, prefix) {
		return nil, "", false
	}
	comma := strings.Index(raw, ",")
	if comma < 0 {
		return nil, "", false
	}
	meta := strings.ToLower(raw[len(prefix):comma])
	if !strings.HasSuffix(meta, ";base64") {
		return nil, "", false
	}
	encoded := strings.Map(func(r rune) rune {
		if r == ' ' || r == '\n' || r == '\r' || r == '\t' || r == '\f' {
			return -1
		}
		return r
	}, raw[comma+1:])
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(data) == 0 || len(data) > maxRemoteImageBytes {
		return nil, "", false
	}
	ext, ok := imageExt(data)
	if !ok {
		return nil, "", false
	}
	return data, ext, true
}

func isRemoteHTTP(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	return (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

var imageHTTPClient = &http.Client{
	Timeout: 25 * time.Second,
	Transport: &http.Transport{
		Proxy:                 nil,
		DialContext:           imageDialer.DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		MaxIdleConns:          8,
		IdleConnTimeout:       30 * time.Second,
	},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		if _, err := validateImageURL(req.URL.String()); err != nil {
			return err
		}
		return nil
	},
}

var imageDialer = &net.Dialer{
	Timeout: 10 * time.Second,
	Control: func(network, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		ip := net.ParseIP(host)
		if ip == nil || !isPublicIP(ip) {
			return errBlockedImageHost
		}
		return nil
	},
}

func fetchRemoteImage(ctx context.Context, raw string) ([]byte, string, error) {
	u, err := validateImageURL(raw)
	if err != nil {
		return nil, "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Accept", "image/png,image/jpeg,image/gif,image/webp,image/avif")
	req.Header.Set("User-Agent", "ReportForPublic/1.0 (+markdown image fetcher)")
	resp, err := imageHTTPClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("image status %d", resp.StatusCode)
	}
	if resp.ContentLength > maxRemoteImageBytes {
		return nil, "", errors.New("image too large")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxRemoteImageBytes+1))
	if err != nil {
		return nil, "", err
	}
	if len(data) > maxRemoteImageBytes {
		return nil, "", errors.New("image too large")
	}
	ext, ok := imageExt(data)
	if !ok {
		return nil, "", errors.New("unsupported image type")
	}
	return data, ext, nil
}

func validateImageURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || u.User != nil {
		return nil, errors.New("invalid image URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, errors.New("image URL must be HTTP(S)")
	}
	if port := u.Port(); port != "" && port != "80" && port != "443" {
		return nil, errors.New("image URL port not allowed")
	}
	u.Fragment = ""
	return u, nil
}

func imageExt(data []byte) (string, bool) {
	switch http.DetectContentType(data) {
	case "image/png":
		return ".png", true
	case "image/jpeg":
		return ".jpg", true
	case "image/gif":
		return ".gif", true
	case "image/webp":
		return ".webp", true
	case "image/avif":
		return ".avif", true
	}
	if len(data) >= 12 && string(data[4:12]) == "ftypavif" {
		return ".avif", true
	}
	return "", false
}

func isPublicIP(ip net.IP) bool {
	if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return false
	}
	ip4 := ip.To4()
	if ip4 != nil {
		if ip4[0] == 100 && ip4[1]&0xc0 == 64 { // 100.64.0.0/10 carrier-grade NAT
			return false
		}
		if ip4[0] == 0 { // 0.0.0.0/8
			return false
		}
	}
	return true
}

func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".rewrite-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func (a *App) handleMedia(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	name := r.PathValue("name")
	if _, ok := a.store.Get(id); !ok || !mediaNameRe.MatchString(name) {
		a.notFound(w, r)
		return
	}
	doc := Document{Type: "image", Ext: filepath.Ext(name)}
	f, err := a.store.OpenMedia(id, name)
	if err != nil {
		a.notFound(w, r)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		a.notFound(w, r)
		return
	}
	w.Header().Set("Content-Type", mimeFor(doc))
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeContent(w, r, name, info.ModTime(), f)
}

// mediaBytesLocked counts downloaded image bytes. The caller must hold s.mu.
func (s *Store) mediaBytesLocked() int64 {
	var total int64
	docs, err := os.ReadDir(s.mediaDir)
	if err != nil {
		return 0
	}
	for _, docDir := range docs {
		if !docDir.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(s.mediaDir, docDir.Name()))
		if err != nil {
			continue
		}
		for _, f := range files {
			if !f.Type().IsRegular() {
				continue
			}
			if info, err := f.Info(); err == nil {
				total += info.Size()
			}
		}
	}
	return total
}

func (s *Store) markdownImageBudget(size int64) int64 {
	if s.maxTotalBytes <= 0 {
		return maxMarkdownMediaBytes
	}
	s.mu.RLock()
	used := s.totalBytesLocked()
	s.mu.RUnlock()
	remaining := s.maxTotalBytes - used - size
	if remaining < 0 {
		return 0
	}
	if remaining > maxMarkdownMediaBytes {
		return maxMarkdownMediaBytes
	}
	return remaining
}

func (s *Store) removeMedia(docID string) {
	_ = os.RemoveAll(filepath.Join(s.mediaDir, docID))
}

func (s *Store) OpenMedia(docID, name string) (*os.File, error) {
	if !mediaNameRe.MatchString(name) {
		return nil, os.ErrNotExist
	}
	return os.Open(filepath.Join(s.mediaDir, docID, name))
}

// LocalizeExistingMarkdown is a one-shot maintenance operation for documents
// uploaded before image localization existed. It rewrites current Markdown
// files and refreshes metadata/search text without changing IDs or timestamps.
func (s *Store) LocalizeExistingMarkdown() (int, error) {
	s.mu.RLock()
	docs := append([]Document(nil), s.docs...)
	s.mu.RUnlock()
	processed := 0
	for _, doc := range docs {
		if doc.Type != "md" {
			continue
		}
		newSize, err := s.localizeMarkdownFile(s.FilePath(doc), doc.ID, s.markdownImageBudget(doc.Size))
		if err != nil {
			log.Printf("localize existing %s: %v", doc.ID, err)
			continue
		}
		doc.Size = newSize
		doc.Excerpt = readExcerpt(s.FilePath(doc), 160)
		body := s.searchText(doc)
		s.mu.Lock()
		for i := range s.docs {
			if s.docs[i].ID == doc.ID {
				s.docs[i] = doc
				s.body[doc.ID] = body
				break
			}
		}
		s.mu.Unlock()
		processed++
	}
	s.mu.Lock()
	err := s.saveLocked()
	s.mu.Unlock()
	return processed, err
}
