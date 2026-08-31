package extdep

import (
	"reflect"
	"strings"
	"testing"
)

func TestValidate_NoDeps(t *testing.T) {
	g := NewGraph()
	g.Register("a", nil)
	g.Register("b", nil)
	if err := g.Validate([]string{"a", "b"}, []string{"a", "b"}); err != nil {
		t.Errorf("Validate = %v, want nil", err)
	}
}

func TestValidate_UnknownKey(t *testing.T) {
	g := NewGraph()
	g.Register("a", []Dependency{{Key: "nope/nope", Kind: RuntimeOptional}})
	err := g.Validate([]string{"a"}, []string{"a"})
	if err == nil {
		t.Fatal("Validate = nil, want unknown-key error")
	}
	if !strings.Contains(err.Error(), "nope/nope") {
		t.Errorf("error = %q, want it to name the unknown key", err)
	}
}

func TestValidate_Cycle(t *testing.T) {
	tests := []struct {
		name    string
		register func(g *Graph)
		want    string // substring of the error
	}{
		{
			name: "two-node cycle",
			register: func(g *Graph) {
				g.Register("a", []Dependency{{Key: "b", Kind: RuntimeRequired}})
				g.Register("b", []Dependency{{Key: "a", Kind: RuntimeRequired}})
			},
			want: "cycle",
		},
		{
			name: "three-node cycle",
			register: func(g *Graph) {
				g.Register("a", []Dependency{{Key: "b", Kind: RuntimeRequired}})
				g.Register("b", []Dependency{{Key: "c", Kind: RuntimeRequired}})
				g.Register("c", []Dependency{{Key: "a", Kind: RuntimeRequired}})
			},
			want: "cycle",
		},
		{
			name: "self dependency",
			register: func(g *Graph) {
				g.Register("a", []Dependency{{Key: "a", Kind: RuntimeRequired}})
			},
			want: "cycle",
		},
		{
			// Cycles are checked over all declared edges, including
			// optional ones.
			name: "cycle via optional edge",
			register: func(g *Graph) {
				g.Register("a", []Dependency{{Key: "b", Kind: RuntimeOptional}})
				g.Register("b", []Dependency{{Key: "a", Kind: RuntimeOptional}})
			},
			want: "cycle",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGraph()
			tt.register(g)
			err := g.Validate([]string{"a", "b", "c"}, []string{"a", "b", "c"})
			if err == nil {
				t.Fatal("Validate = nil, want cycle error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestValidate_NoDAG(t *testing.T) {
	// a -> b -> c is a valid dependency chain (a requires b, b requires c).
	g := NewGraph()
	g.Register("a", []Dependency{{Key: "b", Kind: RuntimeRequired}})
	g.Register("b", []Dependency{{Key: "c", Kind: RuntimeRequired}})
	g.Register("c", nil)
	if err := g.Validate([]string{"a", "b", "c"}, []string{"a", "b", "c"}); err != nil {
		t.Errorf("Validate = %v, want nil", err)
	}
}

func TestValidate_RequiredDepDisabled(t *testing.T) {
	g := NewGraph()
	g.Register("a", []Dependency{{Key: "b", Kind: RuntimeRequired}})
	// b is known but not enabled; only a is registered (running).
	err := g.Validate([]string{"a", "b"}, []string{"a"})
	if err == nil {
		t.Fatal("Validate = nil, want required-dep-disabled error")
	}
	if !strings.Contains(err.Error(), "a requires b") {
		t.Errorf("error = %q, want it to name the dependent and the dep", err)
	}
}

func TestValidate_CompileRequiredDepDisabled(t *testing.T) {
	g := NewGraph()
	g.Register("a", []Dependency{{Key: "b", Kind: CompileRequired}})
	err := g.Validate([]string{"a", "b"}, []string{"a"})
	if err == nil {
		t.Fatal("Validate = nil, want required-dep-disabled error")
	}
	if !strings.Contains(err.Error(), "compile-required") {
		t.Errorf("error = %q, want it to name the kind", err)
	}
}

func TestValidate_OptionalDepDisabled(t *testing.T) {
	g := NewGraph()
	g.Register("a", []Dependency{{Key: "b", Kind: RuntimeOptional}})
	// b disabled: a still starts, the feature is hidden at request time.
	if err := g.Validate([]string{"a", "b"}, []string{"a"}); err != nil {
		t.Errorf("Validate = %v, want nil", err)
	}
}

func TestOrder_DependencyFirst(t *testing.T) {
	g := NewGraph()
	g.Register("a", []Dependency{{Key: "b", Kind: RuntimeRequired}})
	g.Register("b", []Dependency{{Key: "c", Kind: RuntimeRequired}})
	g.Register("c", nil)

	for _, input := range [][]string{
		{"a", "b", "c"},
		{"c", "a", "b"},
		{"b", "c", "a"},
	} {
		out, err := g.Order(input)
		if err != nil {
			t.Fatalf("Order(%v) = error: %v", input, err)
		}
		want := []string{"c", "b", "a"}
		if !reflect.DeepEqual(out, want) {
			t.Errorf("Order(%v) = %v, want %v", input, out, want)
		}
	}
}

func TestOrder_NoDeps_PreservesArgumentOrder(t *testing.T) {
	g := NewGraph()
	in := []string{"z", "m", "a"}
	out, err := g.Order(in)
	if err != nil {
		t.Fatalf("Order = error: %v", err)
	}
	if !reflect.DeepEqual(out, in) {
		t.Errorf("Order = %v, want %v (argument order)", out, in)
	}
}

func TestOrder_OptionalDepNoEdge(t *testing.T) {
	g := NewGraph()
	// a optionally depends on b: no ordering constraint — argument order wins.
	g.Register("a", []Dependency{{Key: "b", Kind: RuntimeOptional}})
	out, err := g.Order([]string{"a", "b"})
	if err != nil {
		t.Fatalf("Order = error: %v", err)
	}
	if !reflect.DeepEqual(out, []string{"a", "b"}) {
		t.Errorf("Order = %v, want [a b] (optional deps impose no order)", out)
	}
}

func TestOrder_DetachedKey(t *testing.T) {
	// A key not present in the ordered set is not an ordering edge.
	g := NewGraph()
	g.Register("a", []Dependency{{Key: "off", Kind: RuntimeRequired}})
	out, err := g.Order([]string{"a", "b"})
	if err != nil {
		t.Fatalf("Order = error: %v", err)
	}
	if !reflect.DeepEqual(out, []string{"a", "b"}) {
		t.Errorf("Order = %v, want [a b]", out)
	}
}

func TestIsEnabled(t *testing.T) {
	g := NewGraph()
	g.Register("a", nil)
	if err := g.Validate([]string{"a", "b"}, []string{"a"}); err != nil {
		t.Fatalf("Validate = %v", err)
	}
	if !g.IsEnabled("a") {
		t.Error("IsEnabled(a) = false, want true")
	}
	if g.IsEnabled("b") {
		t.Error("IsEnabled(b) = true, want false")
	}

	var nilG *Graph
	if nilG.IsEnabled("a") {
		t.Error("nil graph IsEnabled = true, want false")
	}
}

func TestInitializedLifecycle(t *testing.T) {
	g := NewGraph()
	if g.IsInitialized("a") {
		t.Error("IsInitialized(a) before MarkInitialized = true, want false")
	}
	g.MarkInitialized("a")
	if !g.IsInitialized("a") {
		t.Error("IsInitialized(a) after MarkInitialized = false, want true")
	}
	if g.IsInitialized("b") {
		t.Error("IsInitialized(b) = true, want false")
	}

	var nilG *Graph
	if nilG.IsInitialized("a") {
		t.Error("nil graph IsInitialized = true, want false")
	}
}
