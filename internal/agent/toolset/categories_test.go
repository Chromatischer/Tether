package toolset

import (
	"context"
	"encoding/json"
	"testing"

	"tether/internal/tools"
)

func newCategoryTestRegistry() *tools.Registry {
	reg := tools.NewRegistry()
	for _, impl := range DefaultTools() {
		reg.Register(impl.Spec())
	}
	return reg
}

func TestDefaultOnCategoriesAreActive(t *testing.T) {
	reg := newCategoryTestRegistry()
	s := NewSession(reg)

	// exec is on by default (per product decision); memory/scheduling are off.
	wantOn := map[string]bool{
		"tool.search": true, "read": true, "write": true,
		"web-fetch": true, "fetch.summarize": true, "bash": true,
		"subagent.spawn": true, "skill.invoke": true,
		"confirm.request": true,
	}
	for name := range wantOn {
		if !s.IsActive(name) {
			t.Errorf("expected %q active by default", name)
		}
	}
	wantOff := []string{"memory.add", "memory.list", "proactive.run", "self.schedule"}
	for _, name := range wantOff {
		if s.IsActive(name) {
			t.Errorf("expected %q OFF by default", name)
		}
	}
}

func TestEnableCategoryActivatesGroup(t *testing.T) {
	reg := newCategoryTestRegistry()
	s := NewSession(reg)

	enabled, err := s.EnableCategory(tools.CategoryMemory)
	if err != nil {
		t.Fatalf("EnableCategory: %v", err)
	}
	if len(enabled) != 4 {
		t.Fatalf("expected 4 memory tools, got %v", enabled)
	}
	for _, name := range []string{"memory.add", "memory.update", "memory.delete", "memory.list"} {
		if !s.IsActive(name) {
			t.Errorf("expected %q active after enabling memory category", name)
		}
	}

	if _, err := s.EnableCategory("bogus"); err == nil {
		t.Errorf("expected error for unknown category")
	}
}

func TestToolEnableByCategory(t *testing.T) {
	reg := newCategoryTestRegistry()
	s := NewSession(reg)

	raw, _ := json.Marshal(map[string]any{"category": "scheduling"})
	res, err := ToolEnable{}.Execute(context.Background(), s, raw)
	if err != nil {
		t.Fatalf("ToolEnable: %v", err)
	}
	m := res.(map[string]any)
	if m["category"] != "scheduling" {
		t.Errorf("unexpected category echo: %v", m["category"])
	}
	if !s.IsActive("proactive.run") || !s.IsActive("self.schedule") {
		t.Errorf("scheduling tools not active after enable")
	}

	// network=true is only valid for exec.
	rawBad, _ := json.Marshal(map[string]any{"category": "memory", "network": true})
	te := ToolEnable{}
	if _, err := te.Execute(context.Background(), s, rawBad); err == nil {
		t.Errorf("expected error: network only valid for exec")
	}
}

func TestEveryToolCategoryHasMembers(t *testing.T) {
	reg := newCategoryTestRegistry()
	// Every category that has DefaultOn/AlwaysOn semantics should resolve; and
	// each registered tool's category must be a known one.
	for _, c := range tools.AllCategories() {
		_ = reg.ToolsInCategory(c.Name) // smoke: must not panic
	}
	for _, spec := range reg.ListSpecs() {
		if !tools.IsValidCategory(spec.Category) {
			t.Errorf("tool %q has invalid category %q", spec.Name, spec.Category)
		}
	}
}
