package toolset

// DefaultTools returns the full set of built-in tools.
//
// Tools are stateless; dependencies are provided via Session at execution time.
func DefaultTools() map[string]Tool {
	return map[string]Tool{
		"tool.search":     ToolSearch{},
		"tool.enable":     ToolEnable{},
		"tool.describe":   ToolDescribe{},
		"bash":            Bash{},
		"admin.bash":      AdminBash{},
		"bash.host":       HostBash{},
		"code":            Code{},
		"read":            ReadFile{},
		"write":           WriteFile{},
		"edit":            EditFile{},
		"view_image":      ViewImage{},
		"web-search":      WebSearch{},
		"web-fetch":       WebFetch{},
		"fetch.summarize": FetchSummarize{},
		"memory.list":     MemoryList{},
		"memory.add":      MemoryAdd{},
		"memory.delete":   MemoryDelete{},
		"memory.update":   MemoryUpdate{},
		"confirm.request": ConfirmRequest{},
		"confirm.scope":   ConfirmScope{},
		"proactive.run":   ProactiveRun{},
		"self.schedule":   SelfSchedule{},
		"subagent.spawn":  SubagentSpawn{},
		"subagent.status": SubagentStatus{},
		"skill.invoke":    SkillInvoke{},
	}
}
