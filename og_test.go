package main

import (
	"bytes"
	"image/png"
	"testing"
	"time"
)

func TestRenderOGAndCardDimensions(t *testing.T) {
	doc := Document{Title: "A long enough report title for testing", Type: "md", UploadedAt: time.Now()}
	og, err := renderDocOG(doc)
	if err != nil {
		t.Fatal(err)
	}
	if c, err := png.DecodeConfig(bytes.NewReader(og)); err != nil || c.Width != 1200 || c.Height != 630 {
		t.Fatalf("og config: %+v %v", c, err)
	}
	card, err := renderDocCard(doc, true)
	if err != nil {
		t.Fatal(err)
	}
	if c, err := png.DecodeConfig(bytes.NewReader(card)); err != nil || c.Width != 1200 || c.Height != 800 {
		t.Fatalf("card config: %+v %v", c, err)
	}
}
