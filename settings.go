package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const defaultMatcher = "Bash|Grep|mcp__.*"

func cmdInit(args []string) {
	matcher := defaultMatcher
	global := false
	var path string
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--global":
			global = true
		case a == "--with-rtk":
			matcher = "Grep|Read|mcp__.*"
		case a == "--matcher" && i+1 < len(args):
			matcher = args[i+1]
			i++
		case !strings.HasPrefix(a, "--"):
			path = a
		}
	}

	settingsPath := resolveSettings(global, path)
	m := readSettings(settingsPath)

	entry := map[string]interface{}{
		"matcher": matcher,
		"hooks": []interface{}{
			map[string]interface{}{"type": "command", "command": "ctk hook"},
		},
	}
	list := append(filterOutCtk(getPostToolUse(m)), entry)
	setPostToolUse(m, list)

	if err := writeSettings(settingsPath, m); err != nil {
		fmt.Fprintln(os.Stderr, "ctk: failed to write settings:", err)
		os.Exit(1)
	}
	fmt.Printf("Installed ctk hook → %s\n  matcher: %s\n  command: ctk hook\n\n"+
		"Restart Claude Code (or /hooks reload) to activate. Verify with /context.\n",
		settingsPath, matcher)
}

func cmdUninstall(args []string) {
	global := false
	var path string
	for _, a := range args {
		if a == "--global" {
			global = true
		} else if !strings.HasPrefix(a, "--") {
			path = a
		}
	}
	settingsPath := resolveSettings(global, path)
	m := readSettings(settingsPath)
	before := len(getPostToolUse(m))
	list := filterOutCtk(getPostToolUse(m))
	setPostToolUse(m, list)
	_ = writeSettings(settingsPath, m)
	if before-len(list) > 0 {
		fmt.Printf("Removed ctk hook from %s\n", settingsPath)
	} else {
		fmt.Printf("No ctk hook found in %s\n", settingsPath)
	}
}

// --------------------------------------------------------------- settings I/O

func resolveSettings(global bool, path string) string {
	if global {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".claude", "settings.json")
	}
	root := path
	if root == "" {
		root, _ = os.Getwd()
	} else {
		root, _ = filepath.Abs(root)
	}
	return filepath.Join(root, ".claude", "settings.json")
}

func readSettings(p string) map[string]interface{} {
	m := map[string]interface{}{}
	if data, err := os.ReadFile(p); err == nil {
		_ = json.Unmarshal(data, &m)
	}
	if m == nil {
		m = map[string]interface{}{}
	}
	return m
}

func getPostToolUse(m map[string]interface{}) []interface{} {
	hooks, _ := m["hooks"].(map[string]interface{})
	if hooks == nil {
		return nil
	}
	arr, _ := hooks["PostToolUse"].([]interface{})
	return arr
}

func setPostToolUse(m map[string]interface{}, list []interface{}) {
	hooks, _ := m["hooks"].(map[string]interface{})
	if hooks == nil {
		hooks = map[string]interface{}{}
		m["hooks"] = hooks
	}
	hooks["PostToolUse"] = list
}

func isCtkEntry(e interface{}) bool {
	entry, ok := e.(map[string]interface{})
	if !ok {
		return false
	}
	hooks, _ := entry["hooks"].([]interface{})
	for _, h := range hooks {
		hm, _ := h.(map[string]interface{})
		if cmd, _ := hm["command"].(string); strings.Contains(cmd, "ctk hook") {
			return true
		}
	}
	return false
}

func filterOutCtk(list []interface{}) []interface{} {
	out := make([]interface{}, 0, len(list))
	for _, e := range list {
		if !isCtkEntry(e) {
			out = append(out, e)
		}
	}
	return out
}

func writeSettings(p string, m map[string]interface{}) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, append(b, '\n'), 0o644)
}
