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
	if p.Owner != fn || z.Owner != fn {
		t.Errorf("p owned by %T, z by %T; both want the function", p.Owner, z.Owner)
	}
	if f.Owner != prog {
		t.Errorf("f is owned by %T, want the program", f.Owner)
	}

	// Slots are per owner frame.
	if p.Slot == z.Slot {
		t.Errorf("parameter and local share slot %d in one frame", p.Slot)
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
			t.Fatalf("no binding for '%s'", name)
		}
		return out.Capture
	}

	if got := captureOf("captured"); got != CaptureClosure {
		t.Errorf("captured: got capture mode %v, want CaptureClosure", got)
	}
	for _, name := range []string{"plain", "param", "local"} {
		if got := captureOf(name); got != CaptureNone {
			t.Errorf("%s: got capture mode %v, want CaptureNone", name, got)
		}
	}
}

// After resolution a use of a name is a binding id plus a hop count, stamped
// onto the node itself.
func TestUsesAreStamped(t *testing.T) {
	const src = `let x = 1
let f = func () do
  x
end`
	prog, info := resolveOK(t, src)

	var use *ast.Identifier
	ast.Walk(prog, func(n ast.Node) bool {
		if id, ok := n.(*ast.Identifier); ok && id.Value == "x" {
			if _, isDecl := info.Declaration(id); !isDecl {
				use = id
			}
		}
		return true
	})
	if use == nil {
		t.Fatal("found no use of x")
	}
	if use.Binding == 0 {
		t.Error("the use of x carries no binding id")
	}
	if use.Hops != 1 {
		t.Errorf("x is one frame up from its use, got %d hops", use.Hops)
	}
	ref, ok := info.Lookup(use)
	if !ok || ref.Binding == nil || ref.Binding.ID != use.Binding {
		t.Error("the refs table and the node disagree about the binding")
	}
}

// A failed resolution taints the whole Info: the evaluator must not accept
// the ids that did get stamped.
func TestFailureTaintsInfo(t *testing.T) {
	_, info, bag := resolve(t, "let x = 1\nx = 2")
	if !bag.HasErrors() {
		t.Fatal("expected a diagnostic")
	}
	if info.OK() {
		t.Error("Info reports OK after a failed resolution")
	}
}
