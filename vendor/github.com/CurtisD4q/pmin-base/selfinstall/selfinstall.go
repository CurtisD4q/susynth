// Package selfinstall lets a program install itself: `prog install` copies
// the running program onto the PATH, with the files that came beside it (an
// Emacs file, a man page), and `prog uninstall` removes exactly what it put
// there. After that the downloaded archive or the cloned repository can be
// deleted and the program run from anywhere.
//
// It never asks for more rights than it has: `install --system` writes to
// /usr/local only when it may, and otherwise prints the sudo command for the
// person to run. Nothing is downloaded and nothing but the files listed is
// touched; the program's own settings and data are left alone by uninstall.
package selfinstall

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// Program describes what to install.
type Program struct {
	// Name is the command's name, as typed.
	Name string
	// Files ship with the program and are copied into its data folder. Each
	// is looked for beside the program as given (a path in the repository,
	// such as "emacs/cairn.el", for a program built there) and then by its
	// name alone (a release archive keeps them at its top). One found in
	// neither place is skipped with a note.
	Files []string
	// Man, optional, is a manual page in roff, written as man1/<Name>.1.
	Man string
	// After, optional, is printed after a successful install, such as how
	// to load the Emacs file. "{data}" in it is replaced by the data folder.
	After string
}

// Places is where an install puts things.
type Places struct {
	Bin, Data, Man string
}

// manifest is the file in the data folder that lists what install wrote.
const manifest = "installed.txt"

// For gives the places for an install into prefix ("" for the person's own
// folders: ~/.local/bin, ~/.local/share/<name>, ~/.local/share/man/man1).
func For(name, prefix string, getenv func(string) string) (Places, error) {
	if prefix != "" {
		if !filepath.IsAbs(prefix) {
			return Places{}, errors.New("the prefix must be a full path")
		}
		return Places{Bin: filepath.Join(prefix, "bin"), Data: filepath.Join(prefix, "share", name), Man: filepath.Join(prefix, "share", "man", "man1")}, nil
	}
	home := getenv("HOME")
	if home == "" {
		return Places{}, errors.New("HOME is not set, so there is no folder of your own to install into")
	}
	share := getenv("XDG_DATA_HOME")
	if share == "" || !filepath.IsAbs(share) {
		share = filepath.Join(home, ".local", "share")
	}
	return Places{Bin: filepath.Join(home, ".local", "bin"), Data: filepath.Join(share, name), Man: filepath.Join(share, "man", "man1")}, nil
}

// Handle runs `install` or `uninstall` when args starts with one of them,
// and says whether it did and the exit code. Flags: --system (into
// /usr/local) and --prefix DIR.
func Handle(p Program, args []string, out, errOut io.Writer) (handled bool, code int) {
	if len(args) == 0 || (args[0] != "install" && args[0] != "uninstall") {
		return false, 0
	}
	prefix, err := prefixOf(args[1:])
	if err != nil {
		fmt.Fprintf(errOut, "%s %s: %v\n", p.Name, args[0], err)
		return true, 2
	}
	if runtime.GOOS == "windows" {
		fmt.Fprintf(errOut, "%s %s: not on Windows; put %s.exe in a folder on your PATH instead\n", p.Name, args[0], p.Name)
		return true, 1
	}
	pl, err := For(p.Name, prefix, os.Getenv)
	if err == nil {
		exe, e := os.Executable()
		if e == nil {
			exe, e = filepath.EvalSymlinks(exe)
		}
		if e != nil {
			err = fmt.Errorf("cannot find this program's own file: %w", e)
		} else if args[0] == "install" {
			err = Install(p, exe, pl, out)
		} else {
			err = Uninstall(p, pl, out)
		}
	}
	if err != nil {
		fmt.Fprintf(errOut, "%s %s: %v\n", p.Name, args[0], err)
		if errors.Is(err, os.ErrPermission) && prefix != "" {
			fmt.Fprintf(errOut, "Run it as the administrator: sudo %s %s\n", quoteArgs(os.Args[0]), quoteArgs(args...))
		}
		return true, 1
	}
	return true, 0
}

func prefixOf(args []string) (string, error) {
	prefix := ""
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--system":
			prefix = "/usr/local"
		case a == "--prefix" && i+1 < len(args):
			i++
			prefix = args[i]
		case strings.HasPrefix(a, "--prefix="):
			prefix = strings.TrimPrefix(a, "--prefix=")
		default:
			return "", fmt.Errorf("unknown option %q (use --system or --prefix DIR)", a)
		}
	}
	return prefix, nil
}

// Install copies exe and the program's files into pl and records what it
// wrote, so Uninstall removes exactly that. It is safe to run again: it
// replaces its own files and nothing else.
func Install(p Program, exe string, pl Places, out io.Writer) error {
	var wrote []string
	bin := filepath.Join(pl.Bin, p.Name)
	same, _ := sameFile(exe, bin)
	if !same {
		if err := copyFile(exe, bin, 0o755); err != nil {
			return err
		}
	}
	wrote = append(wrote, bin)
	fmt.Fprintf(out, "Installed %s\n", bin)
	beside := filepath.Dir(exe)
	for _, f := range p.Files {
		src := filepath.Join(beside, f)
		if _, err := os.Stat(src); err != nil {
			src = filepath.Join(beside, filepath.Base(f))
			if _, err := os.Stat(src); err != nil {
				fmt.Fprintf(out, "Skipped %s: it is not beside the program\n", f)
				continue
			}
		}
		dst := filepath.Join(pl.Data, filepath.Base(f))
		if err := copyFile(src, dst, 0o644); err != nil {
			return err
		}
		wrote = append(wrote, dst)
		fmt.Fprintf(out, "Installed %s\n", dst)
	}
	if p.Man != "" {
		dst := filepath.Join(pl.Man, p.Name+".1")
		if err := writeFile(dst, []byte(p.Man), 0o644); err != nil {
			return err
		}
		wrote = append(wrote, dst)
		fmt.Fprintf(out, "Installed %s\n", dst)
	}
	if err := writeFile(filepath.Join(pl.Data, manifest), []byte(strings.Join(wrote, "\n")+"\n"), 0o644); err != nil {
		return err
	}
	if !onPath(pl.Bin, os.Getenv("PATH")) {
		fmt.Fprintf(out, "\n%s is not on your PATH. Add this line to ~/.profile (or ~/.bashrc, ~/.zshrc) and open a new terminal:\n  export PATH=\"%s:$PATH\"\n", pl.Bin, pl.Bin)
	}
	if p.After != "" {
		fmt.Fprintln(out, "\n"+strings.ReplaceAll(p.After, "{data}", pl.Data))
	}
	fmt.Fprintf(out, "\nDone. You can delete the download or the cloned folder and run %s from anywhere.\n", p.Name)
	return nil
}

// Uninstall removes what Install recorded in pl, and the data folder if
// that leaves it empty. Files it did not write are left alone.
func Uninstall(p Program, pl Places, out io.Writer) error {
	list := filepath.Join(pl.Data, manifest)
	f, err := os.Open(list)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%s is not installed in %s (no %s)", p.Name, pl.Bin, list)
	}
	if err != nil {
		return err
	}
	var paths []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if l := strings.TrimSpace(sc.Text()); l != "" {
			paths = append(paths, l)
		}
	}
	f.Close()
	allowed := []string{pl.Bin, pl.Data, pl.Man}
	sort.Strings(paths)
	for _, x := range paths {
		if !inside(x, allowed) {
			fmt.Fprintf(out, "Left %s: it is outside the install folders\n", x)
			continue
		}
		if err := os.Remove(x); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		fmt.Fprintf(out, "Removed %s\n", x)
	}
	if err := os.Remove(list); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	_ = os.Remove(pl.Data) // only succeeds when empty
	fmt.Fprintf(out, "%s is uninstalled. Its settings and data are kept.\n", p.Name)
	return nil
}

func inside(x string, dirs []string) bool {
	x = filepath.Clean(x)
	for _, d := range dirs {
		if filepath.Dir(x) == filepath.Clean(d) {
			return true
		}
	}
	return false
}

func onPath(dir, path string) bool {
	for _, d := range filepath.SplitList(path) {
		if filepath.Clean(d) == filepath.Clean(dir) {
			return true
		}
	}
	return false
}

func sameFile(a, b string) (bool, error) {
	x, err := os.Stat(a)
	if err != nil {
		return false, err
	}
	y, err := os.Stat(b)
	if err != nil {
		return false, err
	}
	return os.SameFile(x, y), nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return writeFile(dst, b, mode)
}

// writeFile writes beside dst and renames, so a running copy of the
// program being replaced keeps working and a failed write leaves the old
// file as it was.
func writeFile(dst string, b []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dst)
}

func quoteArgs(args ...string) string {
	q := make([]string, len(args))
	for i, a := range args {
		if a == "" || strings.ContainsAny(a, " \t'\"$`\\;&|<>*?()[]{}!#~") {
			a = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		}
		q[i] = a
	}
	return strings.Join(q, " ")
}
