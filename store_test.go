package main

import (
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

func TestStoreCreateSearchReplaceDelete(t *testing.T) {
	store, err := NewStore(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := store.Create(strings.NewReader("# Alpha\n\nunique search term"), "alpha.md", "Alpha report", "", []string{"one", "two"}, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if doc.ID == "" || doc.Type != "md" || doc.Title != "Alpha report" {
		t.Fatalf("bad doc: %+v", doc)
	}
	if got := store.List("unique", "", "new"); len(got) != 1 || got[0].ID != doc.ID {
		t.Fatalf("search body: %+v", got)
	}
	if got := store.List("#one", "", "new"); len(got) != 1 || got[0].ID != doc.ID {
		t.Fatalf("hash tag search: %+v", got)
	}
	if got := store.List("tag:TWO", "", "new"); len(got) != 1 || got[0].ID != doc.ID {
		t.Fatalf("tag search: %+v", got)
	}
	if got := store.List("tag:missing", "", "new"); len(got) != 0 {
		t.Fatalf("missing tag matched: %+v", got)
	}
	if st := store.Stats(); st.Count != 1 || st.Bytes == 0 {
		t.Fatalf("stats: %+v", st)
	}

	replaced, err := store.Replace(doc.ID, strings.NewReader("# Beta\n\nreplacement term"), "beta.md", "", "", nil, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if replaced.Title != "Alpha report" {
		t.Fatalf("replace lost title: %+v", replaced)
	}
	if len(replaced.Versions) != 1 {
		t.Fatalf("expected one version, got %+v", replaced.Versions)
	}
	if got := store.List("replacement", "", "new"); len(got) != 1 {
		t.Fatalf("new body not indexed: %+v", got)
	}
	if got := store.List("unique", "", "new"); len(got) != 0 {
		t.Fatalf("old body still indexed: %+v", got)
	}
	v, f, ok := store.OpenVersion(doc.ID, replaced.Versions[0].ID)
	if !ok {
		t.Fatal("version not openable")
	}
	defer f.Close()
	b, _ := io.ReadAll(f)
	if !strings.Contains(string(b), "unique search term") || v.Size == 0 {
		t.Fatalf("bad archived version: %q %+v", b, v)
	}

	if err := store.Delete(doc.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Get(doc.ID); ok {
		t.Fatal("document still present")
	}
	if err := store.Delete(doc.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("second delete: %v", err)
	}
}

func TestStoreQuota(t *testing.T) {
	store, err := NewStore(t.TempDir(), 8)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Create(strings.NewReader("0123456789"), "too-big.txt", "Too big", "", nil, 1<<20)
	if !errors.Is(err, errQuota) {
		t.Fatalf("expected errQuota, got %v", err)
	}
	if store.Stats().Count != 0 {
		t.Fatal("quota rejection left a document")
	}
}
