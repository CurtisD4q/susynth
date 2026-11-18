package mini

import (
	"fmt"
	"strings"
	"testing"

	"github.com/susynth/susynth/internal/frac"
)

// cycleStr renders cycle c as "start:value" tokens.
func cycleStr(t *testing.T, src string, c int64) string {
	t.Helper()
	p, err := Parse(src, nil)
	if err != nil {
		t.Fatalf("%q: %v", src, err)
	}
	var parts []string
	for _, e := range p.Query(frac.Int(c), frac.Int(c+1)) {
		parts = append(parts, fmt.Sprintf("%d/%d:%s", e.Start.Sub(frac.Int(c)).N, e.Start.Sub(frac.Int(c)).D, e.Value))
	}
	return strings.Join(parts, " ")
}

func TestBasics(t *testing.T) {
	cases := []struct {
		src  string
		c    int64
		want string
	}{
		{"bd sn", 0, "0/1:bd 1/2:sn"},
		{"bd*2 [sn cp]", 0, "0/1:bd 1/4:bd 1/2:sn 3/4:cp"},
		{"bd ~ sn ~", 0, "0/1:bd 1/2:sn"},
		{"bd _ sn", 0, "0/1:bd 2/3:sn"},
		{"bd@3 sn", 0, "0/1:bd 3/4:sn"},
		{"bd!2 sn", 0, "0/1:bd 1/3:bd 2/3:sn"},
		{"bd ! sn", 0, "0/1:bd 1/3:bd 2/3:sn"},
		{"<a b c>", 0, "0/1:a"},
		{"<a b c>", 4, "0/1:b"},
		{"<a <b c>>", 3, "0/1:c"},
		{"bd/2", 0, "0/1:bd"},
		{"bd/2", 1, ""},
		{"bd*2 sn", 0, "0/1:bd 1/4:bd 1/2:sn"},
		{"[bd, hh*2]", 0, "0/1:bd 0/1:hh 1/2:hh"},
		{"bd(3,8)", 0, "0/1:bd 3/8:bd 3/4:bd"},
		{"bd(3,8,2)", 0, "1/8:bd 1/2:bd 3/4:bd"},
		{"bd:3 sn", 0, "0/1:bd:3 1/2:sn"},
	}
	for _, tc := range cases {
		if got := cycleStr(t, tc.src, tc.c); got != tc.want {
			t.Errorf("%q cycle %d: got %q want %q", tc.src, tc.c, got, tc.want)
		}
	}
}

func TestSpanQuery(t *testing.T) {
	p, _ := Parse("bd sn", nil)
	evs := p.Query(frac.New(1, 2), frac.New(5, 2))
	if len(evs) != 4 { // sn@.5 bd@1 sn@1.5 bd@2
		t.Fatalf("got %d events", len(evs))
	}
}

func TestDegradeDeterministic(t *testing.T) {
	a := cycleStr(t, "hh*8?", 3)
	b := cycleStr(t, "hh*8?", 3)
	if a != b {
		t.Fatal("degrade is not deterministic")
	}
	if n := len(strings.Fields(cycleStr(t, "hh*8?", 0))); n == 0 || n == 8 {
		t.Fatalf("expected a partial drop, got %d", n)
	}
}

func TestErrors(t *testing.T) {
	for _, src := range []string{"[bd sn", "bd sn]", "<a b", "bd*", "bd(3)", "_ bd", "bd$"} {
		if _, err := Parse(src, nil); err == nil {
			t.Errorf("%q: expected error", src)
		}
	}
}

func TestFunctions(t *testing.T) {
	mk := func(src string) *Pattern {
		p, err := Parse(src, nil)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	show := func(p *Pattern, c int64) string {
		var parts []string
		for _, e := range p.Query(frac.Int(c), frac.Int(c+1)) {
			d := e.Start.Sub(frac.Int(c))
			parts = append(parts, fmt.Sprintf("%d/%d:%s", d.N, d.D, e.Value))
		}
		return strings.Join(parts, " ")
	}
	check := func(name string, p *Pattern, err error, c int64, want string) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := show(p, c); got != want {
			t.Errorf("%s cycle %d: got %q want %q", name, c, got, want)
		}
	}
	p := mk("a b c d")
	check("rev", p.Rev(), nil, 0, "0/1:d 1/4:c 1/2:b 3/4:a")
	f, err := p.Fast(2)
	check("fast 2", f, err, 0, "0/1:a 1/8:b 1/4:c 3/8:d 1/2:a 5/8:b 3/4:c 7/8:d")
	check("rotL 0.25", p.Rot(0.25), nil, 0, "0/1:b 1/4:c 1/2:d 3/4:a")
	check("rotR 0.25", p.Rot(-0.25), nil, 0, "0/1:d 1/4:a 1/2:b 3/4:c")
	it, err := p.Iter(4)
	check("iter 4 c0", it, err, 0, "0/1:a 1/4:b 1/2:c 3/4:d")
	check("iter 4 c1", it, err, 1, "0/1:b 1/4:c 1/2:d 3/4:a")
	check("iter 4 c3", it, err, 3, "0/1:d 1/4:a 1/2:b 3/4:c")
	pal := mk("a b").Palindrome()
	check("palindrome c0", pal, nil, 0, "0/1:a 1/2:b")
	check("palindrome c1", pal, nil, 1, "0/1:b 1/2:a")
	ev, err := mk("a b").Every(3, func(q *Pattern) (*Pattern, error) { return q.Rev(), nil })
	check("every c0", ev, err, 0, "0/1:b 1/2:a")
	check("every c1", ev, err, 1, "0/1:a 1/2:b")
	check("every c3", ev, err, 3, "0/1:b 1/2:a")
	pl, err := mk("a b").Ply(2)
	check("ply 2", pl, err, 0, "0/1:a 1/4:a 1/2:b 3/4:b")
	dg, err := mk("h*16").DegradeBy(0.5)
	if err != nil || len(dg.Query(frac.Int(0), frac.Int(1))) >= 16 {
		t.Errorf("degradeBy should drop some of 16: %v", err)
	}
	if _, err := p.Fast(0); err == nil {
		t.Error("fast 0 should fail")
	}
}
