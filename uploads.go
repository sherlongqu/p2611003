package main

import (
	"bytes"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
)

var errBadUpload = errors.New("bad upload request")

// uploadPayload is one parsed upload request, ready for the store.
type uploadPayload struct {
	reader      io.Reader
	filename    string
	title       string
	description string
	tags        []string
	maxBytes    int64
}

func (p uploadPayload) Close() {
	if c, ok := p.reader.(io.Closer); ok {
		c.Close()
	}
}

func (a *App) readUpload(w http.ResponseWriter, r *http.Request) (uploadPayload, error) {
	maxBytes := a.cfg.MaxUploadMB << 20
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes+(2<<20))
	p := uploadPayload{maxBytes: maxBytes}

	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/form-data") {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			return p, err
		}
		file, hdr, err := r.FormFile("file")
		if err != nil {
			return p, errBadUpload
		}
		if hdr.Size > maxBytes {
			file.Close()
			return p, errTooLarge
		}
		p.reader = file
		p.filename = hdr.Filename
		p.title = r.FormValue("title")
		p.description = r.FormValue("description")
		p.tags = parseTags(r.FormValue("tags"))
		return p, nil
	}

	filename := strings.TrimSpace(r.URL.Query().Get("filename"))
	if filename == "" {
		filename = strings.TrimSpace(r.Header.Get("X-Filename"))
	}
	if filename == "" {
		return p, errBadUpload
	}
	p.reader = r.Body
	p.filename = filename
	p.title = r.URL.Query().Get("title")
	p.description = r.URL.Query().Get("description")
	p.tags = parseTags(r.URL.Query().Get("tags"))
	return p, nil
}

func (a *App) receiveUpload(w http.ResponseWriter, r *http.Request) (Document, *inlineReport, error) {
	p, err := a.readUpload(w, r)
	if err != nil {
		return Document{}, nil, err
	}
	defer p.Close()
	reader, rep, err := a.prepareDocument(p)
	if err != nil {
		return Document{}, nil, err
	}
	doc, err := a.store.Create(reader, p.filename, p.title, p.description, p.tags, p.maxBytes)
	return doc, rep, err
}

func (a *App) receiveReplace(w http.ResponseWriter, r *http.Request, id string) (Document, *inlineReport, error) {
	p, err := a.readUpload(w, r)
	if err != nil {
		return Document{}, nil, err
	}
	defer p.Close()
	reader, rep, err := a.prepareDocument(p)
	if err != nil {
		return Document{}, nil, err
	}
	doc, err := a.store.Replace(id, reader, p.filename, p.title, p.description, p.tags, p.maxBytes)
	return doc, rep, err
}

// prepareDocument inlines external assets in HTML uploads before they are stored.
func (a *App) prepareDocument(p uploadPayload) (io.Reader, *inlineReport, error) {
	_, kind, ok := detectKind(cleanFilename(p.filename))
	if !ok || kind != "html" {
		return p.reader, nil, nil
	}
	data, err := io.ReadAll(io.LimitReader(p.reader, p.maxBytes+1))
	if err != nil {
		return nil, nil, err
	}
	if int64(len(data)) > p.maxBytes {
		return nil, nil, errTooLarge
	}
	out, rep := a.inlineHTMLDocument(data)
	if rep.empty() {
		return bytes.NewReader(data), nil, nil
	}
	return bytes.NewReader(out), &rep, nil
}

func uploadError(err error) (int, string) {
	var maxErr *http.MaxBytesError
	switch {
	case errors.As(err, &maxErr):
		return http.StatusRequestEntityTooLarge, "File exceeds the size limit."
	case errors.Is(err, errTooLarge):
		return http.StatusRequestEntityTooLarge, "File exceeds the size limit."
	case errors.Is(err, errUnsupported):
		return http.StatusUnsupportedMediaType, "That file type is not supported."
	case errors.Is(err, errEmptyFile):
		return http.StatusBadRequest, "The file is empty."
	case errors.Is(err, errQuota):
		return http.StatusInsufficientStorage, "Storage quota exceeded. Delete a document or raise MAX_TOTAL_MB."
	case errors.Is(err, errBadUpload):
		return http.StatusBadRequest, "Choose a file to upload."
	}
	log.Printf("upload error: %v", err)
	return http.StatusBadRequest, "Upload failed. Check the file and try again."
}
