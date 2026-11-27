package main

import (
	"strings"
	"testing"
)

// Guard the features that were lost when a stale local mirror overwrote the
// server CSS. These are intentionally cheap string checks on embedded assets.
func TestStaticRegressionGuards(t *testing.T) {
	css, err := embedded.ReadFile("static/style.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		".pdf-fallback",
		"@media (hover: none) and (pointer: coarse)",
		".toc-wrap.is-scrollable",
		"margin-left: auto",
	} {
		if !strings.Contains(string(css), want) {
			t.Errorf("style.css lost %q", want)
		}
	}

	view, err := embedded.ReadFile("templates/view.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`class="pdf-fallback"`,
		`class="toc-wrap" tabindex="0"`,
		"and .Doc.Versions",
	} {
		if !strings.Contains(string(view), want) {
			t.Errorf("view.html lost %q", want)
		}
	}
	for _, unwanted := range []string{"doc-back", "Keep reading", "relatedDocs"} {
		if strings.Contains(string(view), unwanted) {
			t.Errorf("view.html still contains removed feature %q", unwanted)
		}
	}
}
