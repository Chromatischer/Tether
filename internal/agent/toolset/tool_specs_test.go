package toolset

import (
	"testing"

	"tether/internal/tools"
)

func TestToolSpecs_AreWellFormed(t *testing.T) {
	defs := DefaultTools()
	if len(defs) < 5 {
		t.Fatalf("expected default tools")
	}
	for name, impl := range defs {
		spec := impl.Spec()
		if spec.Name != name {
			t.Fatalf("tool %q: spec.Name mismatch: %q", name, spec.Name)
		}
		if spec.Summary == "" {
			t.Fatalf("tool %q: missing Summary", name)
		}
		if !tools.IsValidCategory(spec.Category) {
			t.Fatalf("tool %q: missing or invalid category %q", name, spec.Category)
		}
		if spec.InputSchema == nil {
			t.Fatalf("tool %q: missing InputSchema", name)
		}
		if spec.OutputSchema == nil {
			t.Fatalf("tool %q: missing OutputSchema", name)
		}
		if len(spec.Examples) < 1 {
			t.Fatalf("tool %q: must have at least one Example", name)
		}
	}
}
