package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	stdhtml "html"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

var (
	errUnsupported = errors.New("unsupported file type")
	errEmptyFile   = errors.New("empty file")
	errTooLarge    = errors.New("file too large")
	errQuota       = errors.New("storage quota exceeded")
)

// Version is one archived copy of a document. The current file is not a
// version; versions only exist after a replace operation.
type Version struct {
	ID       string    `json:"id"`
	Filename string    `json:"filename"`
	Ext      string    `json:"ext"`
	Type     string    `json:"type"`
	Size     int64     `json:"size"`
	SavedAt  time.Time `json:"saved_at"`
}

// Document is one published file plus the metadata shown in the library.
type Document struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Filename    string    `json:"filename"`
	Ext         string    `json:"ext"`
	Type        string    `json:"type"` // pdf | html | md | txt | image
	Size        int64     `json:"size"`
	UploadedAt  time.Time `json:"uploaded_at"`
	Description string    `json:"description,omitempty"`
	Tags        []string  `json:"tags,omitempty"`
	Excerpt     string    `json:"excerpt,omitempty"`
	Versions    []Version `json:"versions,omitempty"`
}

// Stats is the small summary used by the studio, API and sitemap.
type Stats struct {
	Count  int
	Bytes  int64
	Latest time.Time
}

// Store keeps documents in one JSON index and the bytes under files/.
type Store struct {
	mu            sync.RWMutex
	docs          []Document
	body          map[string]string
	dir           string
	filesDir      string
	versionsDir   string
	mediaDir      string
	metaPath      string
	maxTotalBytes int64
	fetchImage    imageFetcher
}

func NewStore(dir string, maxTotalBytes int64) (*Store, error) {
	s := &Store{
		dir:           dir,
		filesDir:      filepath.Join(dir, "files"),
		versionsDir:   filepath.Join(dir, "files", "versions"),
		mediaDir:      filepath.Join(dir, "media"),
		metaPath:      filepath.Join(dir, "metadata.json"),
		body:          map[string]string{},
		maxTotalBytes: maxTotalBytes,
		fetchImage:    fetchRemoteImage,
	}
	if err := os.MkdirAll(s.filesDir, 0o755); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	if err := os.MkdirAll(s.versionsDir, 0o755); err != nil {
		return nil, fmt.Errorf("create versions directory: %w", err)
	}
	if err := os.MkdirAll(s.mediaDir, 0o755); err != nil {
		return nil, fmt.Errorf("create media directory: %w", err)
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) load() error {
	data, err := os.ReadFile(s.metaPath)
	if errors.Is(err, os.ErrNotExist) {
		s.docs = []Document{}
		s.body = map[string]string{}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read metadata: %w", err)
	}
	if len(data) == 0 {
		s.docs = []Document{}
		s.body = map[string]string{}
		return nil
	}
	var docs []Document
	if err := json.Unmarshal(data, &docs); err != nil {
		return fmt.Errorf("parse metadata: %w", err)
	}
	for i := range docs {
		if docs[i].Ext == "" {
			if ext, kind, ok := detectKind(docs[i].Filename); ok {
				docs[i].Ext, docs[i].Type = ext, kind
			}
		}
	}
	sort.SliceStable(docs, func(i, j int) bool {
		return docs[i].UploadedAt.After(docs[j].UploadedAt)
	})
	s.docs = docs
	s.rebuildSearchIndex()
	return nil
}

func (s *Store) saveLocked() error {
	data, err := json.MarshalIndent(s.docs, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, ".metadata-*.json")
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
	return os.Rename(tmpName, s.metaPath)
}

const maxSearchBytes = 1 << 20

var (
	dataURLRe   = regexp.MustCompile(`(?is)data:image/[a-z0-9.+-]+;base64,[A-Za-z0-9+/=\s]+`)
	rawBlockRe  = regexp.MustCompile(`(?is)<(script|style)[^>]*>.*?</(script|style)>`)
	searchTagRe = regexp.MustCompile(`(?s)<[^>]*>`)
)

// rebuildSearchIndex loads searchable text for the current documents. PDFs and
// images are not text-extracted; their metadata remains searchable.
func (s *Store) rebuildSearchIndex() {
	s.body = make(map[string]string, len(s.docs))
	for _, d := range s.docs {
		s.body[d.ID] = s.searchText(d)
	}
}

// searchText returns lower-cased plain text suitable for substring search.
// It is capped so one huge document cannot dominate memory.
func (s *Store) searchText(d Document) string {
	if d.Type == "pdf" || d.Type == "image" {
		return ""
	}
	f, err := os.Open(s.FilePath(d))
	if err != nil {
		return ""
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxSearchBytes))
	if err != nil {
		return ""
	}
	t := string(b)
	switch d.Type {
	case "html":
		t = rawBlockRe.ReplaceAllString(t, " ")
		t = searchTagRe.ReplaceAllString(t, " ")
		t = stdhtml.UnescapeString(t)
	case "md":
		t = dataURLRe.ReplaceAllString(t, " ")
		t = rawBlockRe.ReplaceAllString(t, " ")
		t = searchTagRe.ReplaceAllString(t, " ")
		t = stdhtml.UnescapeString(t)
	}
	return strings.ToLower(t)
}

// totalBytesLocked includes current files and archived versions. The caller
// must hold s.mu.
func (s *Store) totalBytesLocked() int64 {
	var total int64
	for _, d := range s.docs {
		total += d.Size
		for _, v := range d.Versions {
			total += v.Size
		}
	}
	total += s.mediaBytesLocked()
	return total
}

// List returns a filtered snapshot, newest first by default.
func (s *Store) List(q, kind, sortBy string) []Document {
	s.mu.RLock()
	defer s.mu.RUnlock()

	q = strings.TrimSpace(q)
	tagQuery := ""
	lower := strings.ToLower(q)
	switch {
	case strings.HasPrefix(lower, "tag:"):
		tagQuery = strings.TrimSpace(q[len("tag:"):])
	case strings.HasPrefix(q, "#"):
		tagQuery = strings.TrimSpace(q[1:])
	}
	if tagQuery != "" {
		q = ""
	}
	q = strings.ToLower(q)
	out := make([]Document, 0, len(s.docs))
	for _, d := range s.docs {
		if kind != "" && d.Type != kind {
			continue
		}
		if tagQuery != "" {
			found := false
			for _, tag := range d.Tags {
				if strings.Contains(strings.ToLower(tag), strings.ToLower(tagQuery)) {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		if q != "" {
			hay := strings.ToLower(d.Title + " " + d.Description + " " + d.Filename + " " + strings.Join(d.Tags, " ") + " " + s.body[d.ID])
			if !strings.Contains(hay, q) {
				continue
			}
		}
		out = append(out, d)
	}

	switch sortBy {
	case "old":
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
	case "title":
		sort.SliceStable(out, func(i, j int) bool {
			return strings.ToLower(out[i].Title) < strings.ToLower(out[j].Title)
		})
	}
	return out
}

func (s *Store) Stats() Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st := Stats{Count: len(s.docs)}
	for _, d := range s.docs {
		st.Bytes += d.Size
		if d.UploadedAt.After(st.Latest) {
			st.Latest = d.UploadedAt
		}
	}
	st.Bytes += s.mediaBytesLocked()
	return st
}

func (s *Store) Get(id string) (Document, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, d := range s.docs {
		if d.ID == id {
			return d, true
		}
	}
	return Document{}, false
}

// Create streams r to disk and publishes it. maxBytes <= 0 disables the cap.
func (s *Store) Create(r io.Reader, filename, title, description string, tags []string, maxBytes int64) (Document, error) {
	filename = cleanFilename(filename)
	ext, kind, ok := detectKind(filename)
	if !ok {
		return Document{}, errUnsupported
	}

	tmp, err := os.CreateTemp(s.filesDir, ".incoming-*")
	if err != nil {
		return Document{}, err
	}
	tmpName := tmp.Name()
	published := false
	defer func() {
		tmp.Close()
		if !published {
			os.Remove(tmpName)
		}
	}()

	reader := r
	if maxBytes > 0 {
		reader = io.LimitReader(r, maxBytes+1)
	}
	size, err := io.Copy(tmp, reader)
	if err != nil {
		return Document{}, err
	}
	if maxBytes > 0 && size > maxBytes {
		return Document{}, errTooLarge
	}
	if size == 0 {
		return Document{}, errEmptyFile
	}
	if err := tmp.Sync(); err != nil {
		return Document{}, err
	}
	if err := tmp.Close(); err != nil {
		return Document{}, err
	}

	if s.maxTotalBytes > 0 {
		s.mu.RLock()
		used := s.totalBytesLocked()
		s.mu.RUnlock()
		if used+size > s.maxTotalBytes {
			return Document{}, errQuota
		}
	}

	id := newID()
	dest := filepath.Join(s.filesDir, id+ext)
	if err := os.Rename(tmpName, dest); err != nil {
		return Document{}, err
	}
	published = true

	title = limitRunes(strings.TrimSpace(title), 200)
	if title == "" {
		title = strings.TrimSuffix(filename, ext)
	}
	description = limitRunes(strings.TrimSpace(description), 1000)
	if tags == nil {
		tags = []string{}
	}

	doc := Document{
		ID:          id,
		Title:       title,
		Filename:    filename,
		Ext:         ext,
		Type:        kind,
		Size:        size,
		UploadedAt:  time.Now(),
		Description: description,
		Tags:        tags,
	}
	if kind == "md" {
		if newSize, err := s.localizeMarkdownFile(dest, id, s.markdownImageBudget(size)); err != nil {
			log.Printf("localize markdown %s: %v", id, err)
		} else {
			size = newSize
			doc.Size = size
		}
	}
	if description == "" && (kind == "md" || kind == "txt") {
		doc.Excerpt = readExcerpt(dest, 160)
	}

	body := s.searchText(doc)

	s.mu.Lock()
	if s.maxTotalBytes > 0 && s.totalBytesLocked()+size > s.maxTotalBytes {
		s.mu.Unlock()
		os.Remove(dest)
		s.removeMedia(doc.ID)
		return Document{}, errQuota
	}
	s.docs = append(s.docs, doc)
	s.body[doc.ID] = body
	sort.SliceStable(s.docs, func(i, j int) bool {
		return s.docs[i].UploadedAt.After(s.docs[j].UploadedAt)
	})
	err = s.saveLocked()
	s.mu.Unlock()
	if err != nil {
		os.Remove(dest)
		s.removeMedia(doc.ID)
		return Document{}, fmt.Errorf("save index: %w", err)
	}
	return doc, nil
}

// Replace swaps the bytes of an existing document while keeping its ID and URL.
// Metadata is only updated when a non-empty value is supplied.
func (s *Store) Replace(id string, r io.Reader, filename, title, description string, tags []string, maxBytes int64) (Document, error) {
	s.mu.RLock()
	var current Document
	found := false
	for _, d := range s.docs {
		if d.ID == id {
			current, found = d, true
			break
		}
	}
	s.mu.RUnlock()
	if !found {
		return Document{}, os.ErrNotExist
	}

	name := cleanFilename(filename)
	if name == "" {
		name = current.Filename
	}
	ext, kind, ok := detectKind(name)
	if !ok {
		return Document{}, errUnsupported
	}

	tmp, err := os.CreateTemp(s.filesDir, ".incoming-*")
	if err != nil {
		return Document{}, err
	}
	tmpName := tmp.Name()
	published := false
	defer func() {
		tmp.Close()
		if !published {
			os.Remove(tmpName)
		}
	}()

	reader := r
	if maxBytes > 0 {
		reader = io.LimitReader(r, maxBytes+1)
	}
	size, err := io.Copy(tmp, reader)
	if err != nil {
		return Document{}, err
	}
	if maxBytes > 0 && size > maxBytes {
		return Document{}, errTooLarge
	}
	if size == 0 {
		return Document{}, errEmptyFile
	}
	if err := tmp.Sync(); err != nil {
		return Document{}, err
	}
	if err := tmp.Close(); err != nil {
		return Document{}, err
	}

	if s.maxTotalBytes > 0 {
		s.mu.RLock()
		used := s.totalBytesLocked()
		s.mu.RUnlock()
		if used+size > s.maxTotalBytes {
			return Document{}, errQuota
		}
	}

	archived, archiveErr := s.archiveVersion(current)
	if archiveErr != nil && !errors.Is(archiveErr, os.ErrNotExist) {
		return Document{}, archiveErr
	}

	dest := filepath.Join(s.filesDir, id+ext)
	if err := os.Rename(tmpName, dest); err != nil {
		if archiveErr == nil {
			_ = os.Remove(s.versionPath(id, archived))
		}
		return Document{}, err
	}
	published = true
	if oldPath := filepath.Join(s.filesDir, id+current.Ext); oldPath != dest {
		defer os.Remove(oldPath)
	}

	doc := current
	doc.Filename = name
	doc.Ext = ext
	doc.Type = kind
	doc.Size = size
	doc.UploadedAt = time.Now()
	if t := limitRunes(strings.TrimSpace(title), 200); t != "" {
		doc.Title = t
	}
	if d := limitRunes(strings.TrimSpace(description), 1000); d != "" {
		doc.Description = d
	}
	if tags != nil {
		doc.Tags = tags
	}
	if kind == "md" {
		if newSize, err := s.localizeMarkdownFile(dest, id, s.markdownImageBudget(size)); err != nil {
			log.Printf("localize markdown %s: %v", id, err)
		} else {
			size = newSize
			doc.Size = size
		}
	}
	switch {
	case kind == "md" || kind == "txt":
		doc.Excerpt = readExcerpt(dest, 160)
	default:
		doc.Excerpt = ""
	}
	if archiveErr == nil {
		doc.Versions = append([]Version{archived}, doc.Versions...)
		s.pruneVersions(&doc)
	}
	body := s.searchText(doc)

	s.mu.Lock()
	for i := range s.docs {
		if s.docs[i].ID == id {
			s.docs[i] = doc
			break
		}
	}
	s.body[id] = body
	sort.SliceStable(s.docs, func(i, j int) bool {
		return s.docs[i].UploadedAt.After(s.docs[j].UploadedAt)
	})
	err = s.saveLocked()
	s.mu.Unlock()
	if err != nil {
		return Document{}, fmt.Errorf("save index: %w", err)
	}
	return doc, nil
}
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, d := range s.docs {
		if d.ID != id {
			continue
		}
		s.docs = append(s.docs[:i], s.docs[i+1:]...)
		delete(s.body, d.ID)
		if err := s.saveLocked(); err != nil {
			return err
		}
		_ = os.Remove(filepath.Join(s.filesDir, d.ID+d.Ext))
		_ = os.RemoveAll(filepath.Join(s.versionsDir, d.ID))
		s.removeMedia(d.ID)
		return nil
	}
	return os.ErrNotExist
}

// archiveVersion copies the current file into the version store before it is
// replaced. It returns os.ErrNotExist when the current file is already gone.
func (s *Store) archiveVersion(doc Document) (Version, error) {
	src := s.FilePath(doc)
	if _, err := os.Stat(src); err != nil {
		return Version{}, err
	}
	dir := filepath.Join(s.versionsDir, doc.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Version{}, err
	}
	v := Version{
		ID:       time.Now().Format("20060102T150405") + "-" + newID()[:6],
		Filename: doc.Filename,
		Ext:      doc.Ext,
		Type:     doc.Type,
		Size:     doc.Size,
		SavedAt:  time.Now(),
	}
	if err := copyFile(src, filepath.Join(dir, v.ID+v.Ext)); err != nil {
		return Version{}, err
	}
	return v, nil
}

const maxVersions = 5

func (s *Store) pruneVersions(doc *Document) {
	for len(doc.Versions) > maxVersions {
		last := doc.Versions[len(doc.Versions)-1]
		_ = os.Remove(s.versionPath(doc.ID, last))
		doc.Versions = doc.Versions[:len(doc.Versions)-1]
	}
}

func (s *Store) versionPath(docID string, v Version) string {
	return filepath.Join(s.versionsDir, docID, v.ID+v.Ext)
}

// OpenVersion returns an archived file and its metadata.
func (s *Store) OpenVersion(docID, versionID string) (Version, *os.File, bool) {
	doc, ok := s.Get(docID)
	if !ok {
		return Version{}, nil, false
	}
	for _, v := range doc.Versions {
		if v.ID != versionID {
			continue
		}
		f, err := os.Open(s.versionPath(docID, v))
		if err != nil {
			return Version{}, nil, false
		}
		return v, f, true
	}
	return Version{}, nil, false
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	syncErr := out.Sync()
	closeErr := out.Close()
	if copyErr != nil {
		os.Remove(dst)
		return copyErr
	}
	if syncErr != nil {
		os.Remove(dst)
		return syncErr
	}
	if closeErr != nil {
		os.Remove(dst)
		return closeErr
	}
	return nil
}

func (s *Store) FilePath(d Document) string {
	return filepath.Join(s.filesDir, d.ID+d.Ext)
}

func (s *Store) Open(d Document) (*os.File, error) {
	return os.Open(s.FilePath(d))
}

func newID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func detectKind(name string) (ext, kind string, ok bool) {
	ext = strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".pdf":
		return ext, "pdf", true
	case ".html", ".htm":
		return ext, "html", true
	case ".md", ".markdown":
		return ext, "md", true
	case ".txt":
		return ext, "txt", true
	case ".png", ".jpg", ".jpeg", ".webp", ".gif", ".avif", ".svg":
		return ext, "image", true
	}
	return "", "", false
}

func cleanFilename(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = filepath.Base(name)
	name = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 || r == '/' {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(strings.Trim(name, "."))
	if name == "" {
		return "document"
	}
	if utf8.RuneCountInString(name) > 120 {
		ext := filepath.Ext(name)
		base := strings.TrimSuffix(name, ext)
		name = limitRunes(base, 110) + ext
	}
	return name
}

func limitRunes(s string, n int) string {
	if n <= 0 || utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n])
}

func readExcerpt(path string, limit int) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	buf := make([]byte, 8192)
	n, _ := f.Read(buf)
	if n <= 0 {
		return ""
	}
	return plainExcerpt(buf[:n], limit)
}

func normalizeKind(k string) string {
	switch strings.ToLower(strings.TrimSpace(k)) {
	case "pdf":
		return "pdf"
	case "html", "htm":
		return "html"
	case "md", "markdown":
		return "md"
	case "txt", "text":
		return "txt"
	case "image", "img":
		return "image"
	}
	return ""
}

func parseTags(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	raw = strings.NewReplacer("\n", ",", " ", ",").Replace(raw)
	seen := map[string]bool{}
	out := []string{}
	for _, p := range strings.Split(raw, ",") {
		p = strings.TrimSpace(strings.TrimPrefix(p, "#"))
		if p == "" || seen[strings.ToLower(p)] {
			continue
		}
		seen[strings.ToLower(p)] = true
		out = append(out, limitRunes(p, 24))
		if len(out) == 5 {
			break
		}
	}
	return out
}
