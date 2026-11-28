// Package packs downloads free sample libraries into the samples folder:
// recordings released under CC0 that are too big to build into susynth.
// Each library becomes a set of banks (sample :tr808_bd, 2).
package packs

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Pack is a sample library that can be installed.
type Pack struct {
	Name    string // what to type: susynth samples install tr808
	Repo    string // GitHub owner/repo it comes from
	Title   string
	About   string
	Licence string
	Note    string // anything to know before using it
}

// Packs lists the libraries susynth can install.
var Packs = []Pack{
	{Name: "tr808", Repo: "tidalcycles/sounds-tr808-fischer", Title: "Roland TR-808",
		About:   "the TR-808 drum machine, every drum at every setting (116 files, about 13 MB)",
		Licence: "CC0 1.0",
		Note:    "released as CC0 by the TidalCycles project; whether the original recordist agreed to CC0 has not been confirmed"},
	{Name: "vcsl", Repo: "sgossner/VCSL", Title: "Versilian Community Sample Library",
		About:   "about 4,200 recordings of acoustic and electronic instruments: pianos, zithers, drums, percussion, winds (large: check your free disk space)",
		Licence: "CC0 1.0"},
	{Name: "vsco", Repo: "sgossner/VSCO-2-CE", Title: "VSCO 2 Community Edition",
		About:   "about 3,200 orchestral recordings: strings, brass, woodwinds, keys, percussion (large: check your free disk space)",
		Licence: "CC0 1.0"},
}

// Find returns the pack called name.
func Find(name string) (Pack, bool) {
	for _, p := range Packs {
		if p.Name == strings.ToLower(name) {
			return p, true
		}
	}
	return Pack{}, false
}

// BaseURL is where a pack's archive is downloaded from (a test can point it
// at its own server): BaseURL + repo + "/zip/HEAD".
var BaseURL = "https://codeload.github.com/"

const (
	maxDownload = 8 << 30  // bytes
	maxFile     = 32 << 20 // the samples folder skips bigger files anyway
)

// Result says what an install did.
type Result struct {
	Banks, Files, Skipped int
	Licence               string // where the licence was written
}

// Install downloads a pack and unpacks its .wav and .flac files into
// samplesDir, one bank per folder. Files that are already there are kept,
// so installing again finishes a download that was cut short.
func Install(ctx context.Context, p Pack, samplesDir string, progress func(string)) (Result, error) {
	var res Result
	if progress == nil {
		progress = func(string) {}
	}
	if err := os.MkdirAll(samplesDir, 0o755); err != nil {
		return res, err
	}
	tmp, err := os.CreateTemp(samplesDir, ".download-*.zip")
	if err != nil {
		return res, err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	url := BaseURL + p.Repo + "/zip/HEAD"
	progress("downloading " + url)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return res, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return res, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return res, fmt.Errorf("download failed: %s", resp.Status)
	}
	n, err := io.Copy(tmp, &meter{r: io.LimitReader(resp.Body, maxDownload+1), progress: progress})
	if err != nil {
		return res, fmt.Errorf("download failed: %w", err)
	}
	if n > maxDownload {
		return res, errors.New("download is larger than expected; stopped")
	}
	progress(fmt.Sprintf("downloaded %.1f MB, unpacking", float64(n)/1e6))

	zr, err := zip.OpenReader(tmp.Name())
	if err != nil {
		return res, fmt.Errorf("the download isn't a zip archive: %w", err)
	}
	defer zr.Close()
	names := newNamer(p.Name)
	banks := map[string]bool{}
	var licence []byte
	for _, f := range zr.File {
		rel := f.Name
		if i := strings.IndexByte(rel, '/'); i >= 0 { // drop the archive's top folder
			rel = rel[i+1:]
		}
		if f.FileInfo().IsDir() || rel == "" || strings.Contains(rel, "..") || strings.HasPrefix(rel, "/") {
			continue // folders, and paths that try to leave the archive
		}
		if strings.EqualFold(rel, "LICENSE") || strings.EqualFold(rel, "LICENSE.txt") {
			licence, _ = readAll(f, 1<<20)
			continue
		}
		ext := strings.ToLower(path.Ext(rel))
		if ext != ".wav" && ext != ".flac" {
			continue
		}
		if f.UncompressedSize64 > maxFile {
			res.Skipped++
			continue
		}
		bank, file := names.place(rel)
		dir := filepath.Join(samplesDir, bank)
		dst := filepath.Join(dir, file)
		banks[bank] = true
		if st, err := os.Stat(dst); err == nil && st.Size() == int64(f.UncompressedSize64) {
			res.Files++
			continue
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return res, err
		}
		if err := extract(f, dst); err != nil {
			return res, fmt.Errorf("%s: %w", rel, err)
		}
		res.Files++
		if res.Files%250 == 0 {
			progress(fmt.Sprintf("%d files", res.Files))
		}
	}
	res.Banks = len(banks)
	if res.Files == 0 {
		return res, errors.New("the download had no .wav or .flac files")
	}
	ld := filepath.Join(samplesDir, "licences")
	if err := os.MkdirAll(ld, 0o755); err == nil {
		res.Licence = filepath.Join(ld, p.Name+".txt")
		text := fmt.Sprintf("%s\nfrom https://github.com/%s\nlicence: %s\n", p.Title, p.Repo, p.Licence)
		if p.Note != "" {
			text += "note: " + p.Note + "\n"
		}
		if len(licence) > 0 {
			text += "\n" + string(licence)
		}
		os.WriteFile(res.Licence, []byte(text), 0o644)
	}
	return res, nil
}

func readAll(f *zip.File, limit int64) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, limit))
}

// extract writes one archive entry to dst, by way of a temporary file so a
// cut-short install never leaves half a sample behind.
func extract(f *zip.File, dst string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	part := dst + ".part"
	out, err := os.Create(part)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, io.LimitReader(rc, maxFile+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(part)
		return err
	}
	return os.Rename(part, dst)
}

var notName = regexp.MustCompile(`[^a-z0-9]+`)

// clean turns a folder or file name into one a bank may use.
func clean(s string) string {
	return strings.Trim(notName.ReplaceAllString(strings.ToLower(s), "_"), "_")
}

// namer gives each folder in a pack its own bank name (pack_folder, at most
// 31 characters, as sample bank names are) and each file a safe name.
type namer struct {
	pack  string
	banks map[string]string // folder -> bank
	taken map[string]bool
	files map[string]bool // bank/file names used
}

func newNamer(pack string) *namer {
	return &namer{pack: pack, banks: map[string]string{}, taken: map[string]bool{}, files: map[string]bool{}}
}

func (n *namer) place(rel string) (bank, file string) {
	dir := path.Dir(rel)
	bank, ok := n.banks[dir]
	if !ok {
		parts := strings.Split(dir, "/")
		var keep []string
		for i := len(parts) - 1; i >= 0 && len(keep) < 2; i-- { // the last two folders say what it is
			if c := clean(parts[i]); c != "" {
				keep = append([]string{c}, keep...)
			}
		}
		base := n.pack
		if dir != "." && len(keep) > 0 {
			base += "_" + strings.Join(keep, "_")
		}
		base = trimTo(base, 31)
		bank = base
		for k := 2; n.taken[bank]; k++ {
			s := fmt.Sprintf("_%d", k)
			bank = trimTo(base, 31-len(s)) + s
		}
		n.taken[bank] = true
		n.banks[dir] = bank
	}
	ext := strings.ToLower(path.Ext(rel))
	stem := clean(strings.TrimSuffix(path.Base(rel), path.Ext(rel)))
	if stem == "" {
		stem = "sample"
	}
	file = stem + ext
	for k := 2; n.files[bank+"/"+file]; k++ {
		file = fmt.Sprintf("%s_%d%s", stem, k, ext)
	}
	n.files[bank+"/"+file] = true
	return bank, file
}

func trimTo(s string, n int) string {
	if len(s) > n {
		s = strings.TrimRight(s[:n], "_")
	}
	return s
}

// meter reports download progress every few seconds.
type meter struct {
	r        io.Reader
	n        int64
	last     time.Time
	progress func(string)
}

func (m *meter) Read(b []byte) (int, error) {
	k, err := m.r.Read(b)
	m.n += int64(k)
	if time.Since(m.last) > 3*time.Second {
		m.last = time.Now()
		m.progress(fmt.Sprintf("%.1f MB", float64(m.n)/1e6))
	}
	return k, err
}
