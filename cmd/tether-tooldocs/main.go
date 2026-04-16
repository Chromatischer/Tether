package main

import (
	"fmt"

	"tether/internal/agent/toolset"
	"tether/internal/tools"
)

func main() {
	impls := toolset.DefaultTools()
	specs := make([]tools.ToolSpec, 0, len(impls))
	for _, impl := range impls {
		specs = append(specs, impl.Spec())
	}
	fmt.Print(tools.RenderToolsMarkdown(specs))
}
