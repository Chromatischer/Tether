package tools

import "testing"

func TestRegistry_ListSortedAndSearch(t *testing.T) {
	r := NewRegistry()
	r.Register(ToolSpec{Name: "b", Summary: "bbb", InputSchema: map[string]any{"type": "object"}})
	r.Register(ToolSpec{Name: "a", Summary: "alpha", InputSchema: map[string]any{"type": "object"}})

	list := r.List()
	if len(list) != 2 || list[0].Name != "a" || list[1].Name != "b" {
		t.Fatalf("unexpected sorted list: %+v", list)
	}

	res := r.Search("alp")
	if len(res) != 1 || res[0].Name != "a" {
		t.Fatalf("unexpected search results: %+v", res)
	}

	res = r.Search("")
	if len(res) != 2 {
		t.Fatalf("expected full list on empty query")
	}
}
