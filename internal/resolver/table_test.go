package resolver

import (
	"testing"

	"github.com/fadion/aria/internal/ast"
)

// The binding table is the evaluator's picture of the program: every declared
// name has a dense id, a declaration span, a mutability, an owner frame and a
// slot, and the ids are what the AST carries after resolution.
func TestBindingTable(t *testing.T) {
	const src = `let x = 1
var y = 2
let f = func (p) do
  let z = x + p
  z
end`
	prog, info := resolveOK(t, src)

	// Collect the declarations in source order.
	var lets []*ast.Let
	var vars []*ast.Var
	var fn *ast.Function
	ast.Walk(prog, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.Let:
			lets = append(lets, n)
		case *ast.Var:
			vars = append(vars, n)
		case *ast.Function:
			fn = n
		}
		return true
	})
	if len(lets) != 3 || len(vars) != 1 || fn == nil {
		t.Fatalf("expected 3 lets, 1 var and a function, got %d lets, %d vars", len(lets), len(vars))
	}

	// Ids are dense and positive, and the table hands back the same binding
	// the declaration map does.
	seen := map[int]bool{}
	check := func(id *ast.Identifier) *Binding {
		t.Helper()
		b, ok := info.Declaration(id)
		if !ok {
			t.Fatalf("'%s' has no declaration entry", id.Value)
		}
		if b.ID <= 0 {
			t.Errorf("'%s' has id %d; ids are 1-based", id.Value, b.ID)
		}
		if seen[b.ID] {
			t.Errorf("id %d handed out twice", b.ID)
		}
		seen[b.ID] = true
		if got := info.Binding(b.ID); got != b {
			t.Errorf("table lookup for id %d returned a different binding", b.ID)
		}
		if id.Binding != b.ID {
			t.Errorf("node for '%s' carries id %d, table says %d", id.Value, id.Binding, b.ID)
		}
		return b
	}

	x := check(lets[0].Name)
	y := check(vars[0].Name)
	f := check(lets[1].Name)
	p := check(fn.Parameters[0].Name)
	z := check(fn.Body.Nodes[0].(*ast.Let).Name)

	// Mutability and kind come from the declaration keyword.
	if x.Mutable || !y.Mutable {
		t.Errorf("let mutable=%v, var mutable=%v", x.Mutable, y.Mutable)
	}
	if p.Kind != KindParam {
		t.Errorf("parameter has kind %v", p.Kind)
	}

	// The declaration span is where the name was written.
	if got := src[y.Decl.Start:y.Decl.End]; got != "y" {
		t.Errorf("y's declaration span covers %q, want %q", got, "y")
	}

	// Owner frames: globals belong to the program, the parameter and the
	// local to the function.
	if x.Owner != prog {
		t.Errorf("global x is owned by %T, want the program", x.Owner)
	}
	if f.Owner != prog || y.Owner != prog {
		t.Errorf("globals owned by %T and %T, want the program", f.Owner, y.Owner)
	}
	if p.Owner != fn || z.Owner != fn {
		t.Errorf("p owned by %T, z by %T; both want the function", p.Owner, z.Owner)
	}

	// Slots are per owner frame: the parameter and the local share one frame
	// and must not share a slot.
	if p.Slot == z.Slot {
		t.Errorf("parameter and local share slot %d in one frame", p.Slot)
	}

	// The use of x inside the function points at the global binding, one
	// frame out: the body is resolved directly in the function's own scope.
	use := fn.Body.Nodes[0].(*ast.Let).Value.(*ast.Infix).Left.(*ast.Identifier)
	ref, ok := info.Lookup(use)
	if !ok {
		t.Fatal("the use of x inside the function has no ref")
	}
	if ref.Binding != x {
		t.Error("the use of x did not resolve to the global binding")
	}
	if use.Binding != x.ID || use.Hops != 1 {
		t.Errorf("use carries (id=%d, hops=%d), want (%d, 1)", use.Binding, use.Hops, x.ID)
	}
}

// A name a nested function refers to is marked captured; one that never
// leaves its frame is not.
func TestCaptureModes(t *testing.T) {
	const src = `let captured = 1
let plain = 2
let f = func (param) do
  let local = param
  captured + local
end`
	prog, info := resolveOK(t, src)

	captureOf := func(name string) Capture {
		t.Helper()
		var out *Binding
		ast.Walk(prog, func(n ast.Node) bool {
			if id, ok := n.(*ast.Identifier); ok && id.Value == name {
				if b, isDecl := info.Declaration(id); isDecl {
					out = b
				}
			}
			return true
		})
		if out == nil {
			t.Fatalf("no declaration for '%s'", name)
		}
		return out.Capture
	}

	if got := captureOf("captured"); got != CaptureClosure {
		t.Errorf("captured has capture mode %v, want CaptureClosure", got)
	}
	for _, name := range []string{"plain", "param", "local"} {
		if got := captureOf(name); got != CaptureNone {
			t.Errorf("%s has capture mode %v, want CaptureNone", name, got)
		}
	}
}

// A failed resolution taints the table: the evaluator must be able to refuse
// a program whose names are only partially bound.
func TestFailedResolutionIsNotOK(t *testing.T) {
	_, info, bag := resolve(t, "let x = 1\nx = 2")
	if !bag.HasErrors() {
		t.Fatal("expected a diagnostic about assigning a let")
	}
	if info.OK() {
		t.Error("a resolution that reported errors claims to be OK")
	}

	if _, info := resolveOK(t, "let x = 1"); !info.OK() {
		t.Error("a clean resolution is not OK")
	}
}

// Loop variables and parameters share the let representation: immutable, one
// slot each, owned by the loop or the function.
func TestLoopAndParamBindings(t *testing.T) {
	const src = `for i in [1]
  let j = i
end`
	prog, info := resolveOK(t, src)

	var loop *ast.For
	ast.Walk(prog, func(n ast.Node) bool {
		if f, ok := n.(*ast.For); ok {
			loop = f
		}
		return true
	})
	if loop == nil {
		t.Fatal("no loop found")
	}

	i, ok := info.Declaration(loop.Arguments.Elements[0])
	if !ok {
		t.Fatal("the loop variable has no declaration entry")
	}
	if i.Kind != KindLoop || i.Mutable {
		t.Errorf("loop variable: kind=%v mutable=%v, want KindLoop immutable", i.Kind, i.Mutable)
	}
	if i.Owner != loop {
		t.Errorf("loop variable owned by %T, want the loop", i.Owner)
	}

	body := loop.Body.Nodes[0].(*ast.Let)
	j, ok := info.Declaration(body.Name)
	if !ok {
		t.Fatal("the loop body's let has no declaration entry")
	}
	// The body shares the loop's frame, so j takes the slot after i.
	if j.Owner != loop {
		t.Errorf("loop-body let owned by %T, want the loop", j.Owner)
	}
	if j.Slot != i.Slot+1 {
		t.Errorf("j at slot %d, want %d (right after i)", j.Slot, i.Slot+1)
	}
	if got := info.ScopeSize(loop); got != 2 {
		t.Errorf("loop frame size %d, want 2", got)
	}
}
