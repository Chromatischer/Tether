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

func TestRegistry_SearchMatchesKeywordsAcrossMetadata(t *testing.T) {
	r := NewRegistry()
	r.Register(ToolSpec{
		Name:        "bash",
		Summary:     "Run a shell command inside the user sandbox.",
		InputSchema: map[string]any{"type": "object"},
		Tags:        []string{"shell", "sandbox"},
	})
	r.Register(ToolSpec{
		Name:        "proactive.run",
		Summary:     "Run proactive agents for the current user.",
		InputSchema: map[string]any{"type": "object"},
		Tags:        []string{"proactive"},
	})

	for _, query := range []string{"bash shell", "execute run command"} {
		res := r.Search(query)
		if len(res) == 0 {
			t.Fatalf("expected results for query %q", query)
		}
		if res[0].Name != "bash" {
			t.Fatalf("expected bash ranked first for query %q, got %+v", query, res)
		}
	}
}
