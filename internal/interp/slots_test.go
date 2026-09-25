package interp

import (
	"io"
	"strings"
	"testing"

	"github.com/fadion/aria/internal/ast"
	"github.com/fadion/aria/internal/diag"
	"github.com/fadion/aria/internal/parser"
	"github.com/fadion/aria/internal/resolver"
	"github.com/fadion/aria/internal/source"
)

// compileOnly parses and resolves src the way Eval does, without the standard
// library, so the evaluator's counters can be inspected after a run.
func compileOnly(t *testing.T, src string) (*Interp, *ast.Program) {
	t.Helper()
	file := source.NewFile("test.ari", []byte(src))
	bag := diag.New(file)
	prog := parser.New(file, bag).Parse()
	if bag.HasErrors() {
		t.Fatalf("parse: %s", bag.Render())
	}

	i := New(file, nil)
	i.Out, i.Err = io.Discard, io.Discard
	var sink discard
	units, info, ok := i.compile(file, prog, bag, &sink)
	if !ok {
		t.Fatalf("compile: %s", sink.String())
	}
	i.info = info
	if err := i.evalUnits(units); err != nil {
		t.Fatalf("imports: %v", err)
	}
	return i, prog
}

// The hot path for a resolved name is a frame walk of exactly Hops steps and
// one slot index. Nothing may fall back to scanning for a name by string.
func TestResolvedNamesNeverScanTheChain(t *testing.T) {
	const src = `let outer = 1
let level1 = func ()
  let middle = 2
  let level2 = func ()
    let inner = 3
    var total = 0
    for i in 1..100
      total += outer + middle + inner
    end
    total
  end
  level2()
end
level1()`

	i, prog := compileOnly(t, src)
	if _, err := i.Run(prog); err != nil {
		t.Fatalf("run: %v", err)
	}

	s := i.Stats()
	if s.NameScans != 0 {
		t.Errorf("a resolved name fell back to a string lookup %d time(s)", s.NameScans)
	}
	if s.SlotReads == 0 || s.SlotWrites == 0 {
		t.Errorf("expected slot traffic, got reads=%d writes=%d", s.SlotReads, s.SlotWrites)
	}
	if s.Frames == 0 {
		t.Error("expected frames to be allocated")
	}
}

// A program the resolver rejected carries partial binding information at
// best. The evaluator must refuse to run it rather than accept the ids that
// did get stamped.
func TestRefusesToRunAfterResolveFailure(t *testing.T) {
	const src = `println("ran")
let x = 1
x = 2`
	file := source.NewFile("test.ari", []byte(src))
	bag := diag.New(file)
	prog := parser.New(file, bag).Parse()
	if bag.HasErrors() {
		t.Fatalf("parse: %s", bag.Render())
	}

	info := resolver.New(file, bag).Resolve(prog)
	if info.OK() {
		t.Fatal("resolution of an invalid program reported OK")
	}
	if !bag.HasErrors() {
		t.Fatal("expected a diagnostic about assigning a let")
	}

	i := New(file, info)
	var out strings.Builder
	i.Out = &out
	i.Err = io.Discard
	if _, err := i.Run(prog); err == nil {
		t.Fatal("the evaluator ran a program the resolver rejected")
	}
	if out.Len() != 0 {
		t.Errorf("the rejected program produced output: %q", out.String())
	}
}
