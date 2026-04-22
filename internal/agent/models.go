package agent

import (
	"context"
	"strings"
	"time"
)

type modelInfo struct {
	ContextLength int
	Tokenizer     string
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
		}
	}

	a.mu.Lock()
	a.modelInfoCache = cache
	info := cache[model]
	a.mu.Unlock()
	return info
}
