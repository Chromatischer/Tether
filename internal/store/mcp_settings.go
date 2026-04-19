package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

const userSettingMCPEnabledServers = "mcp.enabled_servers"

// GetMCPEnabledServers returns the per-user configured list of enabled MCP servers.
// If the user has not configured anything, ok will be false.
func GetMCPEnabledServers(db *sql.DB, userID int64) (servers []string, ok bool, err error) {
	val, ok, err := GetUserSetting(db, userID, userSettingMCPEnabledServers)
	if err != nil || !ok {
		return nil, ok, err
	}
	val = strings.TrimSpace(val)
	if val == "" {
		return nil, true, nil
	}
	var raw []string
	if err := json.Unmarshal([]byte(val), &raw); err != nil {
		return nil, false, err
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(raw))
	for _, s := range raw {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out, true, nil
}

func SetMCPEnabledServers(db *sql.DB, userID int64, servers []string) error {
	if db == nil {
		return errors.New("db required")
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(servers))
	for _, s := range servers {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	b, _ := json.Marshal(out)
	return SetUserSetting(db, userID, userSettingMCPEnabledServers, string(b))
}
