package agent

import (
	"context"
	"strings"
	"time"
)

type modelInfo struct {
	ContextLength int
	Tokenizer     string
	// Vision reports whether the model accepts image input.
	Vision bool
}

// modelSupportsVision reports whether the given model can accept image input.
// On lookup failure it returns false (fail-safe: the vision tool stays hidden).
func (a *Agent) modelSupportsVision(model string) bool {
	return a.modelInfo(model).Vision
}

func hasImageModality(mods []string) bool {
	for _, m := range mods {
		if strings.EqualFold(strings.TrimSpace(m), "image") {
			return true
		}
	}
	return false
}

func (a *Agent) modelInfo(model string) modelInfo {
	model = strings.TrimSpace(model)
	if model == "" || a == nil || a.llm == nil {
		return modelInfo{}
	}

	a.mu.Lock()
	if a.modelInfoCache != nil {
		if info, ok := a.modelInfoCache[model]; ok {
			a.mu.Unlock()
			return info
		}
	}
	a.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	models, err := a.llm.Models(ctx)
	if err != nil {
		return modelInfo{}
	}

	cache := make(map[string]modelInfo, len(models))
	for _, m := range models {
		id := strings.TrimSpace(m.ID)
		if id == "" {
			continue
		}
		cache[id] = modelInfo{
			ContextLength: m.ContextLength,
			Tokenizer:     strings.TrimSpace(m.Architecture.Tokenizer),
			Vision:        hasImageModality(m.Architecture.InputModalities),
		}
	}

	a.mu.Lock()
	a.modelInfoCache = cache
	info := cache[model]
	a.mu.Unlock()
	return info
}
