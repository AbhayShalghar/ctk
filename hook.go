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
func cmdHook() {
	defer func() { _ = recover() }()

	raw, err := io.ReadAll(os.Stdin)
	if err != nil || len(raw) == 0 {
		return
	}
	var ev struct {
		Cwd        string          `json:"cwd"`
		ToolName   string          `json:"tool_name"`
		ToolOutput json.RawMessage `json:"tool_output"`
	}
	if err := json.Unmarshal(raw, &ev); err != nil {
		return
	}
	// only string outputs are handled; structured results are left untouched
	var out string
	if err := json.Unmarshal(ev.ToolOutput, &out); err != nil {
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

	r, ok := filters.Compress(ev.ToolName, out, cfg)
	if !ok {
		return
	}

	ctkRoot := filepath.Join(cwd, ".ctk")
	cacheDir := filepath.Join(ctkRoot, "cache")
	_ = os.MkdirAll(cacheDir, 0o755)
	sum := sha1.Sum([]byte(out))
	id := hex.EncodeToString(sum[:])[:12]
	file := filepath.Join(cacheDir, id+".txt")
	_ = os.WriteFile(file, []byte(out), 0o644)

	inTok := filters.EstimateTokens(out)
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
			"updatedToolOutput": r.Text + footer,
		},
	})
	_, _ = os.Stdout.Write(resp)
}

// loadConfig starts from defaults and overlays any ctk.config.json found in cwd.
// json.Unmarshal into an existing struct only overwrites the keys present.
func loadConfig(cwd string) filters.Config {
	cfg := filters.DefaultConfig()
	if data, err := os.ReadFile(filepath.Join(cwd, "ctk.config.json")); err == nil {
		_ = json.Unmarshal(data, &cfg)
	}
	return cfg
}
