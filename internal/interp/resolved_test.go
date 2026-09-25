package interp_test

import (
	"strings"
	"testing"

	"github.com/fadion/aria/internal/interp"
)

// The same name declared at four nested levels: each read must see the
// binding of its own scope, which is the whole point of resolving names to
// (frame, slot) rather than looking them up by string.
func TestFourLevelShadowing(t *testing.T) {
	const src = `let x = 1
let f = func ()
  let x = 2
  var seen = []
  if true then
    let x = 3
    for n in [1]
      let x = 4
      seen[] = x
    end
    seen[] = x
  end
  seen[] = x
  seen
end
var out = f()
out[] = x
out`
	if got := str(t, src); got != "[4, 3, 2, 1]" {
		t.Errorf("got %s, want [4, 3, 2, 1]", got)
	}
}

// A closure made in a loop captures that iteration's variables, not a
// variable shared by every iteration. The per-iteration frame is what the
// closure holds.
func TestLoopClosuresCapturePerIteration(t *testing.T) {
	const forSrc = `var fns = []
for i in 1..3
  fns[] = func () do i end
end
fns[0]() * 100 + fns[1]() * 10 + fns[2]()`
	if got := str(t, forSrc); got != "123" {
		t.Errorf("for: got %s, want 123 (a shared variable would give 333)", got)
	}

	const whileSrc = `var fns = []
var n = 0
while n < 3
  n = n + 1
  let v = n * 10
  fns[] = func () do v end
end
fns[0]() + fns[1]() + fns[2]()`
	if got := str(t, whileSrc); got != "60" {
		t.Errorf("while: got %s, want 60", got)
	}
}

// Two top-level functions calling each other resolve through the hoist, and
// each call reads the other through its own binding.
func TestMutualRecursion(t *testing.T) {
	const src = `let isEven = func (n)
  if n == 0 then
    true
  else
    isOdd(n - 1)
  end
end
let isOdd = func (n)
  if n == 0 then
    false
  else
    isEven(n - 1)
  end
end
[isEven(10), isOdd(7), isEven(3)]`
	if got := str(t, src); got != "[true, true, false]" {
		t.Errorf("got %s, want [true, true, false]", got)
	}
}

// Immutability belongs to the binding however it was introduced: let, var,
// parameter and loop variable are the same mechanism after resolution.
func TestImmutableBindingsCannotBeAssigned(t *testing.T) {
	fails(t, "let a = 1\na = 2", "cannot assign to 'a': it is bound with let")
	fails(t, "let f = func (p) do\n  p = 2\nend", "cannot assign to 'p': it is bound with parameter")
	fails(t, "for i in [1]\n  i = 2\nend", "cannot assign to 'i': it is bound with loop variable")
	fails(t, "let a = [1]\na[] = 2", "cannot modify 'a': it is bound with let")

	// A `var` at every level of nesting rebinds fine, through the same path.
	const src = `var a = 1
let f = func ()
  var b = 2
  if true then
    var c = 3
    a = a * 10
    b = b * 10
    c = c * 10
    a + b + c
  end
end
f() + a`
	if got := str(t, src); got != "70" {
		t.Errorf("got %s, want 70", got)
	}
}

// A runtime error unwinds through loops and calls to the rescue, and control
// signals unwind through a try to the construct they belong to.
func TestUnwinding(t *testing.T) {
	// An error raised two calls down is caught by the rescue, with the loop
	// and both calls unwound.
	const caught = `let boom = func () do panic("deep") end
let f = func ()
  try
    boom()
    "not reached"
  rescue e
    e.message
  end
end
f()`
	if got := str(t, caught); got != "deep" {
		t.Errorf("got %s, want deep", got)
	}

	// break and continue pass through a try to the loop they belong to.
	const signals = `var seen = []
for i in 1..6
  try
    if i == 2 then
      continue
    end
    if i == 5 then
      break
    end
    seen[] = i
  rescue e
    seen[] = 0
  end
end
seen`
	if got := str(t, signals); got != "[1, 3, 4]" {
		t.Errorf("got %s, want [1, 3, 4]", got)
	}

	// A return inside a try unwinds to its function, not to the rescue.
	const ret = `let f = func ()
  for i in 1..10
    try
      if i == 3 then
        return i * 10
      end
    rescue e
      return -1
    end
  end
  0
end
f()`
	if got := str(t, ret); got != "30" {
		t.Errorf("got %s, want 30", got)
	}
}

// A program the resolver rejects must not run at all: no partial binding ids
// may be accepted by the evaluator, and no effect may happen.
func TestResolveFailureRunsNothing(t *testing.T) {
	var out strings.Builder
	_, err := interp.Eval("test.ari", `println("ran")
let x = 1
x = 2`, interp.Options{
		Out: &out, Err: &strings.Builder{}, In: strings.NewReader(""), NoStdlib: true,
	})
	if err == nil {
		t.Fatal("assigning a let should have been rejected")
	}
	if !strings.Contains(err.Error(), "cannot assign to 'x'") {
		t.Errorf("error did not name the problem: %v", err)
	}
	if strings.Contains(out.String(), "ran") {
		t.Error("the rejected program produced output")
	}
}
