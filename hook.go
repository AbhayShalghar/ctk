package main

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/abhayshalghar/ctk/internal/filters"
)

// cmdHook is the PostToolUse handler. Contract: on ANY problem it exits 0 with
// no stdout, so Claude Code keeps the original output. It must never crash a
// session, so the whole body is guarded by recover().
//
// Claude Code's PostToolUse event puts the tool's output in `tool_response`,
// whose shape depends on the tool:
//   - MCP tools  → a string
//   - Bash       → { stdout, stderr, interrupted, ... }   (text in .stdout)
//   - Read       → { type, file: { content, ... } }        (text in .file.content)
// We locate the text payload, compress it, and write a *matching* structure
// back via updatedToolOutput (the replacement must match the original type).
func cmdHook() {
	defer func() { _ = recover() }()

	raw, err := io.ReadAll(os.Stdin)
	if err != nil || len(raw) == 0 {
		return
	}
	var ev struct {
		Cwd          string          `json:"cwd"`
		ToolName     string          `json:"tool_name"`
		ToolResponse json.RawMessage `json:"tool_response"`
	}
	if err := json.Unmarshal(raw, &ev); err != nil || len(ev.ToolResponse) == 0 {
		return
	}

	cwd := ev.Cwd
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	cfg := loadConfig(cwd)
	for _, t := range cfg.DisabledTools {
		if t == ev.ToolName {
			return
		}
	}

	text, rebuild, ok := extractText(ev.ToolResponse)
	if !ok {
		return
	}

	r, ok := filters.Compress(ev.ToolName, text, cfg)
	if !ok {
		return
	}

	ctkRoot := filepath.Join(cwd, ".ctk")
	cacheDir := filepath.Join(ctkRoot, "cache")
	_ = os.MkdirAll(cacheDir, 0o755)
	sum := sha1.Sum([]byte(text))
	id := hex.EncodeToString(sum[:])[:12]
	file := filepath.Join(cacheDir, id+".txt")
	_ = os.WriteFile(file, []byte(text), 0o644)

	inTok := filters.EstimateTokens(text)
	outTok := filters.EstimateTokens(r.Text)
	saved := inTok - outTok

	rec, _ := json.Marshal(map[string]interface{}{
		"ts": time.Now().UTC().Format(time.RFC3339), "tool": ev.ToolName,
		"kind": r.Kind, "inTok": inTok, "outTok": outTok, "saved": saved,
	})
	if f, err := os.OpenFile(filepath.Join(ctkRoot, "stats.jsonl"),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		_, _ = f.Write(append(rec, '\n'))
		_ = f.Close()
	}

	footer := fmt.Sprintf("\n\n[ctk %s: %d%% smaller, ~%d tokens saved. Full output: %s]",
		r.Kind, int(r.Gain*100+0.5), saved, file)

	resp, _ := json.Marshal(map[string]interface{}{
		"hookSpecificOutput": map[string]interface{}{
			"hookEventName":     "PostToolUse",
			"updatedToolOutput": rebuild(r.Text + footer),
		},
	})
	_, _ = os.Stdout.Write(resp)
}

// extractText finds the text payload inside a tool_response and returns it along
// with a rebuild func that produces a same-shaped value with the text replaced.
// Returns ok=false for shapes we don't compress (so the original is kept).
func extractText(raw json.RawMessage) (string, func(string) interface{}, bool) {
	// MCP tools: tool_response is a plain string.
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s, func(n string) interface{} { return n }, true
	}

	// MCP content blocks: [{type:"text", text:"..."}]. Compress the largest
	// text block in place; leave any others untouched.
	var arr []interface{}
	if json.Unmarshal(raw, &arr) == nil {
		bestIdx, bestLen := -1, 0
		for i, el := range arr {
			if blk, ok := el.(map[string]interface{}); ok {
				if t, ok := blk["text"].(string); ok && len(t) > bestLen {
					bestLen, bestIdx = len(t), i
				}
			}
		}
		if bestIdx >= 0 {
			blk := arr[bestIdx].(map[string]interface{})
			return blk["text"].(string),
				func(n string) interface{} { blk["text"] = n; return arr }, true
		}
	}

	// Object shapes.
	var m map[string]interface{}
	if json.Unmarshal(raw, &m) == nil {
		// Bash: text in .stdout
		if v, ok := m["stdout"].(string); ok && v != "" {
			return v, func(n string) interface{} { m["stdout"] = n; return m }, true
		}
		// Read: text in .file.content
		if f, ok := m["file"].(map[string]interface{}); ok {
			if v, ok := f["content"].(string); ok && v != "" {
				return v, func(n string) interface{} { f["content"] = n; return m }, true
			}
		}
		// Generic: first sizable string in a known text-bearing key.
		for _, k := range []string{"content", "text", "output", "result"} {
			if v, ok := m[k].(string); ok && v != "" {
				key := k
				return v, func(n string) interface{} { m[key] = n; return m }, true
			}
		}
	}
	return "", nil, false
}

// loadConfig starts from defaults and overlays any ctk.config.json found in cwd.
func loadConfig(cwd string) filters.Config {
	cfg := filters.DefaultConfig()
	if data, err := os.ReadFile(filepath.Join(cwd, "ctk.config.json")); err == nil {
		_ = json.Unmarshal(data, &cfg)
	}
	return cfg
}
