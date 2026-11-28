// Package rubyvm runs real Ruby (CRuby 3.4 compiled to WebAssembly) inside a
// wazero sandbox, with susynth's Sonic Pi runtime loaded. Go talks to it over
// stdin/stdout with one tab-separated line per message (see runtime.rb).
//
// The sandbox has no network and no file system: live code can make sound and
// nothing else. Ruby is compiled once and cached on disk, so later starts are
// quick; Start returns at once and compiles in the background.
package rubyvm

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"github.com/susynth/susynth/internal/synth"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

//go:embed ruby.wasm.gz
var rubyGz []byte

//go:embed stdlib.rb
var stdlibRB string

//go:embed runtime.rb
var runtimeRB string

// Msg is one line from Ruby, split on tabs.
type Msg []string

// Kind is the message type: "ev", "err", "log", "bpm", "thr+", "thr-", "ran"…
func (m Msg) Kind() string {
	if len(m) == 0 {
		return ""
	}
	return m[0]
}

// Arg returns field i (after the kind), or "".
func (m Msg) Arg(i int) string {
	if i+1 < len(m) {
		return m[i+1]
	}
	return ""
}

// ErrStuck means Ruby did not answer in time, so the VM was restarted and
// every running loop was lost. A live_loop that never sleeps is stopped on
// its own inside Ruby and does not take this path; this is only a real hang.
var ErrStuck = errors.New("Ruby stopped responding and was restarted")

// VM is one Ruby sandbox. Calls are serialised.
type VM struct {
	cacheDir string

	call sync.Mutex // one command at a time

	mu      sync.Mutex
	ready   chan struct{}
	err     error
	status  string
	stdin   *io.PipeWriter
	lines   chan string
	cancel  context.CancelFunc
	runtime wazero.Runtime
	gen     int
	synths  []string
	samples []string
}

// Start begins compiling and booting Ruby in the background. cacheDir keeps
// the compiled code between runs ("" = no disk cache).
func Start(cacheDir string) *VM {
	v := &VM{cacheDir: cacheDir}
	v.boot()
	return v
}

// Ready is closed when Ruby can take commands (check Err afterwards).
func (v *VM) Ready() <-chan struct{} {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.ready
}

// IsReady reports whether Ruby is up.
func (v *VM) IsReady() bool {
	select {
	case <-v.Ready():
		return v.Err() == nil
	default:
		return false
	}
}

// Err is the startup error, if Ruby could not start.
func (v *VM) Err() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.err
}

// Status is a short description for the screen.
func (v *VM) Status() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.status
}

var (
	compiled     wazero.CompiledModule
	compiledRT   wazero.Runtime
	compileOnce  sync.Mutex
	compileError error
)

func (v *VM) setStatus(s string) {
	v.mu.Lock()
	v.status = s
	v.mu.Unlock()
}

func (v *VM) boot() {
	v.mu.Lock()
	v.gen++
	gen := v.gen
	v.ready = make(chan struct{})
	v.err = nil
	v.status = "starting Ruby…"
	ready := v.ready
	v.mu.Unlock()

	go func() {
		err := v.instantiate(gen)
		v.mu.Lock()
		if gen != v.gen { // superseded by a restart
			v.mu.Unlock()
			return
		}
		v.err = err
		if err != nil {
			v.status = "Ruby failed to start"
		} else {
			v.status = "Ruby " + rubyVersion
		}
		v.mu.Unlock()
		if err == nil {
			v.sendTables()
		}
		close(ready)
	}()
}

var (
	rubyVersion = "3.4"
	instances   atomic.Int64 // module names must be unique within the runtime
)

// compile turns the embedded Ruby into machine code once per process (and
// once per machine, thanks to the disk cache).
func compile(cacheDir string, status func(string)) (wazero.Runtime, wazero.CompiledModule, error) {
	compileOnce.Lock()
	defer compileOnce.Unlock()
	if compiled != nil || compileError != nil {
		return compiledRT, compiled, compileError
	}
	ctx := context.Background()
	cfg := wazero.NewRuntimeConfig().WithCloseOnContextDone(true)
	if cacheDir != "" {
		if c, err := wazero.NewCompilationCacheWithDir(cacheDir); err == nil {
			cfg = cfg.WithCompilationCache(c)
		}
	}
	rt := wazero.NewRuntimeWithConfig(ctx, cfg)
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, rt); err != nil {
		compileError = err
		return nil, nil, err
	}
	zr, err := gzip.NewReader(bytes.NewReader(rubyGz))
	if err != nil {
		compileError = err
		return nil, nil, err
	}
	wasm, err := io.ReadAll(zr)
	if err != nil {
		compileError = err
		return nil, nil, err
	}
	status("compiling Ruby (first run only)…")
	mod, err := rt.CompileModule(ctx, wasm)
	if err != nil {
		compileError = fmt.Errorf("compile Ruby: %w", err)
		return nil, nil, compileError
	}
	compiled, compiledRT = mod, rt
	return rt, mod, nil
}

func (v *VM) instantiate(gen int) error {
	rt, mod, err := compile(v.cacheDir, v.setStatus)
	if err != nil {
		return err
	}
	v.setStatus("starting Ruby…")
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	lines := make(chan string, 4096)
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		sc := bufio.NewScanner(outR)
		sc.Buffer(make([]byte, 64<<10), 4<<20)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	var stderr bytes.Buffer
	cfg := wazero.NewModuleConfig().
		WithName(fmt.Sprintf("ruby%d", instances.Add(1))).
		WithStdin(inR).WithStdout(outW).WithStderr(&stderr).
		WithArgs("ruby", "--disable-gems", "-W0", "-e", stdlibRB+"\n"+runtimeRB)
	go func() {
		_, err := rt.InstantiateModule(ctx, mod, cfg)
		_ = err
		outW.Close()
	}()

	v.mu.Lock()
	v.stdin, v.lines, v.cancel, v.runtime = inW, lines, cancel, rt
	v.mu.Unlock()

	// the first exchange proves Ruby is listening
	ms, err := v.exchange("ping\n", 20*time.Second)
	if err != nil {
		if s := strings.TrimSpace(stderr.String()); s != "" {
			return fmt.Errorf("Ruby did not start: %s", s)
		}
		return fmt.Errorf("Ruby did not start: %w", err)
	}
	for _, m := range ms {
		if m.Kind() == "pong" {
			rubyVersion = m.Arg(0)
		}
	}
	return nil
}

// exchange writes one command and collects lines up to the "." terminator.
func (v *VM) exchange(cmd string, timeout time.Duration) ([]Msg, error) {
	v.mu.Lock()
	w, lines := v.stdin, v.lines
	v.mu.Unlock()
	if w == nil {
		return nil, errors.New("Ruby is not running")
	}
	done := make(chan error, 1)
	go func() {
		_, err := io.WriteString(w, cmd)
		done <- err
	}()
	var out []Msg
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		select {
		case l, ok := <-lines:
			if !ok {
				return out, errors.New("Ruby exited")
			}
			if l == "." {
				return out, nil
			}
			out = append(out, Msg(strings.Split(l, "\t")))
		case err := <-done:
			if err != nil {
				return out, err
			}
			done = nil
		case <-deadline.C:
			return out, ErrStuck
		}
	}
}

// Call sends one command (with an optional code payload) and returns Ruby's
// messages. If Ruby does not answer within timeout it is killed and
// restarted, and ErrStuck is returned.
func (v *VM) Call(cmd string, payload string, timeout time.Duration) ([]Msg, error) {
	v.call.Lock()
	defer v.call.Unlock()
	<-v.Ready()
	if err := v.Err(); err != nil {
		return nil, err
	}
	ms, err := v.exchange(cmd+"\n"+payload, timeout)
	if errors.Is(err, ErrStuck) || (err != nil && err.Error() == "Ruby exited") {
		v.restart()
		return ms, ErrStuck
	}
	return ms, err
}

func (v *VM) restart() {
	v.mu.Lock()
	if v.cancel != nil {
		v.cancel()
	}
	if v.stdin != nil {
		v.stdin.CloseWithError(errors.New("restart"))
	}
	v.stdin, v.lines, v.cancel = nil, nil, nil
	v.mu.Unlock()
	v.boot()
}

// Close stops Ruby.
func (v *VM) Close() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.gen++
	if v.cancel != nil {
		v.cancel()
	}
	if v.stdin != nil {
		v.stdin.Close()
	}
}

// Convenience wrappers ---------------------------------------------------------

// Run evaluates code. cursor > 0 runs the top-level statement around that
// 1-based line (a whole live_loop, not a blank-line slice); 0 runs everything.
// at is the start time in seconds.
func (v *VM) Run(id int, cursor int, at float64, code string) ([]Msg, error) {
	return v.RunMode(id, cursor, at, "pass", code)
}

// RunMode is Run with a choice of how a live_loop that is already playing
// takes the new code: "swap" hears it from at, continuing the pass it is in;
// "pass" (Sonic Pi's way) starts it on the loop's next pass.
func (v *VM) RunMode(id int, cursor int, at float64, mode string, code string) ([]Msg, error) {
	first, last := 0, 0
	if cursor > 0 {
		var ok bool
		first, last, ok = BlockRange(code, cursor)
		if !ok {
			return []Msg{{"ran", fmt.Sprint(id), "0", "0"}}, nil
		}
	}
	return v.Call(fmt.Sprintf("run\t%d\t%d\t%d\t%.6f\t%s\t%d", id, first, last, at, mode, len(code)), code, 5*time.Second)
}

// Kill stops one live_loop or thread by name. The others keep running.
func (v *VM) Kill(name string) ([]Msg, error) {
	name = strings.ReplaceAll(name, "\t", " ")
	return v.Call("kill\t"+name, "", 2*time.Second)
}

// Check reports syntax errors without running anything.
func (v *VM) Check(code string) ([]Msg, error) {
	return v.Call(fmt.Sprintf("chk\t%d", len(code)), code, 5*time.Second)
}

// Advance runs every thread up to time t and returns what they did.
func (v *VM) Advance(t float64) ([]Msg, error) {
	return v.Call(fmt.Sprintf("adv\t%.6f", t), "", 2*time.Second)
}

// StopAll ends every loop and thread.
func (v *VM) StopAll() ([]Msg, error) { return v.Call("stop", "", 2*time.Second) }

// NewDeck makes the loops that are playing the old set, so a set can be mixed
// in beside them. An old set that is still playing stops.
func (v *VM) NewDeck() ([]Msg, error) { return v.Call("deck", "", 2*time.Second) }

// StopOld stops the old set.
func (v *VM) StopOld() ([]Msg, error) { return v.Call("stopold", "", 2*time.Second) }

// SetTables tells Ruby which synth and sample names exist, so unknown names
// are errors with line numbers. It is safe to call at any time; the tables are
// sent again whenever Ruby restarts.
func (v *VM) SetTables(synths, samples []string) {
	v.mu.Lock()
	v.synths, v.samples = synths, samples
	v.mu.Unlock()
	if v.IsReady() {
		v.call.Lock()
		v.sendTables()
		v.call.Unlock()
	}
}

// sendTables runs while nothing else can talk to Ruby (during boot, or with
// the call lock held).
func (v *VM) sendTables() {
	v.mu.Lock()
	syn, smp := v.synths, v.samples
	v.mu.Unlock()
	v.exchange("syn\t"+strings.Join(syn, ",")+"\n", 2*time.Second)
	v.exchange("cat\t"+synth.Catalogue()+"\n", 2*time.Second)
	v.exchange("smp\t"+strings.Join(smp, ",")+"\n", 2*time.Second)
}
