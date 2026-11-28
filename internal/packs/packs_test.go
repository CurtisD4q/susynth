package packs

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func archive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for name, body := range files {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(body))
	}
	z.Close()
	return b.Bytes()
}

func TestInstall(t *testing.T) {
	data := archive(t, map[string]string{
		"repo-main/LICENSE":                          "CC0 text",
		"repo-main/README.md":                        "not a sample",
		"repo-main/bd/BD0000.WAV":                    "kick",
		"repo-main/bd/BD0010.wav":                    "kick 2",
		"repo-main/Strings/Cello Section/sus/C2.wav": "cello",
		"repo-main/Brass/Horn Section/sus/C2.wav":    "horn",
		"repo-main/top.flac":                         "loose",
		"repo-main/../../escape.wav":                 "must stay inside",
	})
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Path != "/tidalcycles/sounds-tr808-fischer/zip/HEAD" {
			http.NotFound(w, r)
			return
		}
		w.Write(data)
	}))
	defer srv.Close()
	old := BaseURL
	BaseURL = srv.URL + "/"
	defer func() { BaseURL = old }()

	dir := t.TempDir()
	p, _ := Find("TR808")
	res, err := Install(context.Background(), p, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Files != 5 || res.Banks != 4 { // the escaping entry is skipped
		t.Errorf("%+v", res)
	}
	for _, f := range []string{"tr808_bd/bd0000.wav", "tr808_bd/bd0010.wav", "tr808_cello_section_sus/c2.wav",
		"tr808_horn_section_sus/c2.wav", "tr808/top.flac"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("missing %s", f)
		}
	}
	found := false
	filepath.Walk(filepath.Dir(dir), func(path string, _ os.FileInfo, _ error) error {
		found = found || strings.HasSuffix(path, "escape.wav") && !strings.HasPrefix(path, dir)
		return nil
	})
	if found {
		t.Error("an archive path escaped the samples folder")
	}
	lic, _ := os.ReadFile(res.Licence)
	if !strings.Contains(string(lic), "CC0 text") || !strings.Contains(string(lic), "has not been confirmed") {
		t.Errorf("licence file: %q", lic)
	}
	// again: finishes or repeats without harm
	if res2, err := Install(context.Background(), p, dir, nil); err != nil || res2.Files != res.Files {
		t.Errorf("second install %+v %v", res2, err)
	}
	q, _ := Find("vcsl")
	if _, err := Install(context.Background(), q, dir, nil); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("a failed download should say so: %v", err)
	}
}

func TestBankNames(t *testing.T) {
	n := newNamer("vsco")
	long := "Percussion/A Very Long Folder Name Indeed/Another Long One Here/x.wav"
	b1, _ := n.place(long)
	b2, _ := n.place("Other/A Very Long Folder Name Indeed/Another Long One Here/y.wav")
	if len(b1) > 31 || len(b2) > 31 || b1 == b2 {
		t.Errorf("%q %q", b1, b2)
	}
	_, f1 := n.place("Kit/Snare.WAV")
	_, f2 := n.place("Kit/snare.wav")
	if f1 == f2 {
		t.Errorf("two files with one name: %q", f1)
	}
}
