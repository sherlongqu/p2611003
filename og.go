package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"log"
	"net/http"
	"strings"
	"sync"
	"unicode"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

var (
	ogFontOnce sync.Once
	ogRegular  *opentype.Font
	ogBold     *opentype.Font
	ogFontErr  error
)

var (
	ogPaper  = color.RGBA{247, 244, 236, 255}
	ogInk    = color.RGBA{27, 28, 24, 255}
	ogMuted  = color.RGBA{111, 105, 92, 255}
	ogAccent = color.RGBA{178, 59, 46, 255}
	ogLine   = color.RGBA{220, 213, 196, 255}

	cardDarkBG     = color.RGBA{23, 24, 18, 255}
	cardDarkInk    = color.RGBA{242, 237, 226, 255}
	cardDarkMuted  = color.RGBA{184, 176, 161, 255}
	cardDarkLine   = color.RGBA{52, 52, 43, 255}
	cardDarkAccent = color.RGBA{214, 105, 85, 255}
)

func loadOGFonts() {
	regular, err := embedded.ReadFile("assets/fonts/NotoSansSC-Regular.ttf")
	if err != nil {
		ogFontErr = err
		return
	}
	bold, err := embedded.ReadFile("assets/fonts/NotoSansSC-Bold.ttf")
	if err != nil {
		ogFontErr = err
		return
	}
	ogRegular, ogFontErr = opentype.Parse(regular)
	if ogFontErr != nil {
		return
	}
	ogBold, ogFontErr = opentype.Parse(bold)
}

func ogFace(size float64, bold bool) (font.Face, error) {
	ogFontOnce.Do(loadOGFonts)
	if ogFontErr != nil {
		return nil, ogFontErr
	}
	f := ogRegular
	if bold {
		f = ogBold
	}
	return opentype.NewFace(f, &opentype.FaceOptions{
		Size:    size,
		DPI:     72,
		Hinting: font.HintingFull,
	})
}

func textWidth(face font.Face, s string) int {
	return font.MeasureString(face, s).Ceil()
}

func drawText(dst draw.Image, face font.Face, s string, x, y int, c color.Color) {
	d := &font.Drawer{Dst: dst, Src: image.NewUniform(c), Face: face, Dot: fixed.P(x, y)}
	d.DrawString(s)
}

func truncateToWidth(face font.Face, s string, max int) string {
	if textWidth(face, s) <= max {
		return s
	}
	runes := []rune(s)
	for len(runes) > 0 && textWidth(face, string(runes)+"…") > max {
		runes = runes[:len(runes)-1]
	}
	return string(runes) + "…"
}

func wrapText(face font.Face, text string, max int) []string {
	var lines []string
	var line []rune
	flush := func() {
		if len(line) == 0 {
			return
		}
		lines = append(lines, strings.TrimSpace(string(line)))
		line = nil
	}
	for _, r := range text {
		if r == '\n' {
			flush()
			continue
		}
		candidate := string(append(line, r))
		if len(line) > 0 && textWidth(face, candidate) > max {
			idx := -1
			for i := len(line) - 1; i >= 0; i-- {
				if unicode.IsSpace(line[i]) {
					idx = i
					break
				}
			}
			if idx > 0 {
				lines = append(lines, strings.TrimSpace(string(line[:idx])))
				line = []rune(strings.TrimSpace(string(line[idx+1:])))
			} else {
				flush()
			}
		}
		line = append(line, r)
	}
	flush()
	return lines
}

func docTitleLines(face font.Face, doc Document, maxWidth, maxLines int) []string {
	title := strings.TrimSpace(doc.Title)
	if title == "" {
		title = "Untitled document"
	}
	lines := wrapText(face, title, maxWidth)
	if len(lines) > maxLines {
		lines = lines[:maxLines]
		last := strings.TrimRight(lines[maxLines-1], " ,.;:，。；：")
		lines[maxLines-1] = truncateToWidth(face, last+"…", maxWidth)
	} else if len(lines) > 0 {
		lines[len(lines)-1] = truncateToWidth(face, lines[len(lines)-1], maxWidth)
	}
	return lines
}

// renderDocOG is the social preview: document type, title and update time only.
// No logo, brand line, tagline or URL is drawn, so the text stays readable at
// small preview sizes.
func renderDocOG(doc Document) ([]byte, error) {
	const width, height = 1200, 630
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(img, img.Bounds(), image.NewUniform(ogPaper), image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(0, 0, width, 2), image.NewUniform(ogLine), image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(0, height-2, width, height), image.NewUniform(ogLine), image.Point{}, draw.Src)

	typeFace, err := ogFace(20, true)
	if err != nil {
		return nil, err
	}
	defer typeFace.Close()
	titleFace, err := ogFace(64, true)
	if err != nil {
		return nil, err
	}
	defer titleFace.Close()
	metaFace, err := ogFace(20, false)
	if err != nil {
		return nil, err
	}
	defer metaFace.Close()

	kind := strings.ToUpper(typeLabel(doc.Type))
	drawText(img, typeFace, kind, 80, 172, ogAccent)
	draw.Draw(img, image.Rect(80, 188, 80+textWidth(typeFace, kind), 190), image.NewUniform(ogAccent), image.Point{}, draw.Src)

	for i, line := range docTitleLines(titleFace, doc, 1040, 3) {
		drawText(img, titleFace, line, 80, 300+i*80, ogInk)
	}

	draw.Draw(img, image.Rect(80, 520, 1120, 521), image.NewUniform(ogLine), image.Point{}, draw.Src)
	drawText(img, metaFace, "UPDATED "+formatDateTime(doc.UploadedAt), 80, 568, ogMuted)

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// renderDocCard is deliberately separate from renderDocOG. Cards have their own
// 3:2 canvas and a dark variant, so the homepage never places a bright paper OG
// image into the dark theme.
func renderDocCard(doc Document, dark bool) ([]byte, error) {
	const width, height = 1200, 800
	bg, ink, muted, line, accent := ogPaper, ogInk, ogMuted, ogLine, ogAccent
	if dark {
		bg, ink, muted, line, accent = cardDarkBG, cardDarkInk, cardDarkMuted, cardDarkLine, cardDarkAccent
	}
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(img, img.Bounds(), image.NewUniform(bg), image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(0, 0, width, 2), image.NewUniform(line), image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(0, height-2, width, height), image.NewUniform(line), image.Point{}, draw.Src)

	typeFace, err := ogFace(22, true)
	if err != nil {
		return nil, err
	}
	defer typeFace.Close()
	titleFace, err := ogFace(72, true)
	if err != nil {
		return nil, err
	}
	defer titleFace.Close()
	metaFace, err := ogFace(22, false)
	if err != nil {
		return nil, err
	}
	defer metaFace.Close()

	kind := strings.ToUpper(typeLabel(doc.Type))
	drawText(img, typeFace, kind, 80, 156, accent)
	draw.Draw(img, image.Rect(80, 174, 80+textWidth(typeFace, kind), 176), image.NewUniform(accent), image.Point{}, draw.Src)

	for i, line := range docTitleLines(titleFace, doc, 1040, 3) {
		drawText(img, titleFace, line, 80, 300+i*88, ink)
	}

	draw.Draw(img, image.Rect(80, 660, 1120, 661), image.NewUniform(line), image.Point{}, draw.Src)
	drawText(img, metaFace, "UPDATED "+formatDateTime(doc.UploadedAt), 80, 720, muted)

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (a *App) serveDocImage(w http.ResponseWriter, r *http.Request, key string, render func() ([]byte, error)) {
	a.ogMu.Lock()
	cached, ok := a.ogCache[key]
	a.ogMu.Unlock()
	if !ok {
		b, err := render()
		if err != nil {
			log.Printf("image %s: %v", key, err)
			b, _ = embedded.ReadFile("static/og.png")
		}
		if len(b) == 0 {
			http.Error(w, "image unavailable", http.StatusInternalServerError)
			return
		}
		a.ogMu.Lock()
		if len(a.ogCache) > 256 {
			a.ogCache = map[string][]byte{}
		}
		a.ogCache[key] = b
		a.ogMu.Unlock()
		cached = b
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = w.Write(cached)
}

func (a *App) handleOGImage(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(r.PathValue("id"), ".png")
	doc, ok := a.store.Get(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	key := fmt.Sprintf("og|%s|%s|%s|%d", doc.ID, doc.Title, doc.Type, doc.UploadedAt.Unix())
	a.serveDocImage(w, r, key, func() ([]byte, error) { return renderDocOG(doc) })
}

func (a *App) handleCardImage(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(r.PathValue("id"), ".png")
	doc, ok := a.store.Get(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	dark := r.URL.Query().Get("theme") == "dark"
	theme := "light"
	if dark {
		theme = "dark"
	}
	key := fmt.Sprintf("card|%s|%s|%s|%s|%d", theme, doc.ID, doc.Title, doc.Type, doc.UploadedAt.Unix())
	a.serveDocImage(w, r, key, func() ([]byte, error) { return renderDocCard(doc, dark) })
}
