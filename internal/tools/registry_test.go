package tools

import "testing"

func TestRegistry_ListSortedAndSearch(t *testing.T) {
	r := NewRegistry()
	r.Register(ToolInfo{Name: "b", Description: "bbb"})
	r.Register(ToolInfo{Name: "a", Description: "alpha"})

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

func TestDefaultRegistry_HasCoreTools(t *testing.T) {
	r := DefaultRegistry()
	m := map[string]bool{}
	for _, ti := range r.List() {
		m[ti.Name] = true
	}
	for _, name := range []string{"bash", "read", "write", "web-search", "confirm.request"} {
		if !m[name] {
			t.Fatalf("expected tool %q in default registry", name)
		}
	}
}
