// Package filters holds ctk's deterministic, zero-dependency output compressors.
// Behavior mirrors the original JS prototype 1:1 (see test fixtures).
package filters

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Config controls thresholds and caps. Defaults come from DefaultConfig; a
// project ctk.config.json may override any field (JSON tags match the keys).
type Config struct {
	MinBytes      int      `json:"minBytes"`
	MinGain       float64  `json:"minGain"`
	ArrayKeep     int      `json:"arrayKeep"`
	StrMax        int      `json:"strMax"`
	TextHead      int      `json:"textHead"`
	TextTail      int      `json:"textTail"`
	HitsKeep      int      `json:"hitsKeep"`
	SourceFields  []string `json:"sourceFields"`
	GrepPerFile   int      `json:"grepPerFile"`
	DisabledTools []string `json:"disabledTools"`
}

// DefaultConfig matches the JS DEFAULTS.
func DefaultConfig() Config {
	return Config{
		MinBytes:    1200,
		MinGain:     0.15,
		ArrayKeep:   5,
		StrMax:      280,
		TextHead:    80,
		TextTail:    40,
		HitsKeep:    5,
		GrepPerFile: 4,
	}
}

// Result is a successful compression.
type Result struct {
	Kind   string
	Text   string
	Before int
	After  int
	Gain   float64
}

// EstimateTokens is the chars/4 heuristic, ceil division.
func EstimateTokens(s string) int { return (len(s) + 3) / 4 }

// Compress returns a Result and true only when it beats cfg.MinGain; otherwise
// the caller must keep the original output unchanged.
func Compress(toolName, output string, cfg Config) (*Result, bool) {
	if len(output) < cfg.MinBytes {
		return nil, false
	}
	trimmed := strings.TrimLeft(output, " \t\r\n")
	var r *Result
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		r = compressJSON(output, cfg)
	} else {
		r = compressText(output, cfg)
	}
	if r == nil {
		return nil, false
	}
	before, after := len(output), len(r.Text)
	gain := float64(before-after) / float64(before)
	if gain < cfg.MinGain {
		return nil, false
	}
	r.Before, r.After, r.Gain = before, after, gain
	return r, true
}

// ---------------------------------------------------------------- JSON path

func compressJSON(output string, cfg Config) *Result {
	var data interface{}
	if err := json.Unmarshal([]byte(output), &data); err != nil {
		return nil // looked like JSON but isn't — leave it alone
	}
	if m, ok := data.(map[string]interface{}); ok {
		if hits, ok := m["hits"].(map[string]interface{}); ok {
			if _, ok := hits["hits"].([]interface{}); ok {
				return compressOpenSearch(m, cfg)
			}
		}
	}
	b, err := json.Marshal(shrink(data, cfg))
	if err != nil {
		return nil
	}
	return &Result{Kind: "json", Text: string(b)}
}

func compressOpenSearch(m map[string]interface{}, cfg Config) *Result {
	hitsObj, _ := m["hits"].(map[string]interface{})
	hits, _ := hitsObj["hits"].([]interface{})

	var total interface{}
	switch t := hitsObj["total"].(type) {
	case map[string]interface{}:
		total = t["value"]
	default:
		total = t
	}

	keep := cfg.HitsKeep
	if len(hits) < keep {
		keep = len(hits)
	}
	kept := make([]interface{}, 0, keep)
	for i := 0; i < keep; i++ {
		h, _ := hits[i].(map[string]interface{})
		src := h["_source"]
		if len(cfg.SourceFields) > 0 {
			src = project(src, cfg.SourceFields)
		}
		kept = append(kept, map[string]interface{}{
			"_id":     h["_id"],
			"_score":  h["_score"],
			"_source": shrink(src, cfg),
		})
	}

	summary := map[string]interface{}{
		"_ctk":     "opensearch",
		"total":    total,
		"returned": len(hits),
		"shown":    len(kept),
		"omitted":  max(0, len(hits)-len(kept)),
		"hits":     kept,
	}
	if v := m["took"]; v != nil {
		summary["took"] = v
	}
	if agg, ok := m["aggregations"]; ok && agg != nil {
		summary["aggregations"] = shrink(agg, cfg)
	}
	b, err := json.Marshal(summary)
	if err != nil {
		return nil
	}
	return &Result{Kind: "opensearch", Text: string(b)}
}

func project(src interface{}, fields []string) interface{} {
	m, ok := src.(map[string]interface{})
	if !ok {
		return src
	}
	out := map[string]interface{}{}
	for _, f := range fields {
		if v, ok := m[f]; ok {
			out[f] = v
		}
	}
	return out
}

// shrink recursively caps arrays, drops empty values, truncates long strings.
func shrink(v interface{}, cfg Config) interface{} {
	switch val := v.(type) {
	case string:
		r := []rune(val)
		if len(r) > cfg.StrMax {
			return string(r[:cfg.StrMax]) + fmt.Sprintf("…(+%dc)", len(r)-cfg.StrMax)
		}
		return val
	case []interface{}:
		n := len(val)
		keep := cfg.ArrayKeep
		if n < keep {
			keep = n
		}
		out := make([]interface{}, 0, keep+1)
		for i := 0; i < keep; i++ {
			out = append(out, shrink(val[i], cfg))
		}
		if n > cfg.ArrayKeep {
			out = append(out, fmt.Sprintf("…(+%d of %d)", n-cfg.ArrayKeep, n))
		}
		return out
	case map[string]interface{}:
		out := map[string]interface{}{}
		for k, vv := range val {
			if vv == nil {
				continue
			}
			if s, ok := vv.(string); ok && s == "" {
				continue
			}
			if arr, ok := vv.([]interface{}); ok && len(arr) == 0 {
				continue
			}
			if mp, ok := vv.(map[string]interface{}); ok && len(mp) == 0 {
				continue
			}
			out[k] = shrink(vv, cfg)
		}
		return out
	default:
		return v
	}
}

// ---------------------------------------------------------------- text path

func compressText(output string, cfg Config) *Result {
	lines := strings.Split(output, "\n")
	if g := groupGrep(lines, cfg); g != "" {
		return &Result{Kind: "grep", Text: g}
	}
	return &Result{Kind: "text", Text: dedupeText(lines, cfg)}
}

var grepRe = regexp.MustCompile(`^(.+?):(\d+):`)

// groupGrep collapses file:line:match dumps; returns "" if it isn't one.
func groupGrep(lines []string, cfg Config) string {
	order := []string{}
	groups := map[string][]string{}
	matched := 0
	for _, l := range lines {
		if l == "" || l == "--" {
			continue
		}
		m := grepRe.FindStringSubmatch(l)
		if m == nil {
			return "" // not a clean grep dump — fall back to dedupe
		}
		matched++
		file := m[1]
		if _, ok := groups[file]; !ok {
			order = append(order, file)
		}
		groups[file] = append(groups[file], l)
	}
	if matched < 8 {
		return ""
	}
	var out []string
	for _, file := range order {
		ls := groups[file]
		c := cfg.GrepPerFile
		if len(ls) < c {
			c = len(ls)
		}
		out = append(out, ls[:c]...)
		if len(ls) > cfg.GrepPerFile {
			out = append(out, fmt.Sprintf("  …⟨+%d more in %s⟩", len(ls)-cfg.GrepPerFile, file))
		}
	}
	return strings.Join(out, "\n")
}

func dedupeText(lines []string, cfg Config) string {
	var collapsed []string
	prev := ""
	havePrev := false
	run := 0
	blank := false
	flush := func() {
		if !havePrev {
			return
		}
		if run > 1 {
			collapsed = append(collapsed, fmt.Sprintf("%s  ⟨×%d⟩", prev, run))
		} else {
			collapsed = append(collapsed, prev)
		}
	}
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			if !blank {
				flush()
				havePrev, prev, run = false, "", 0
				collapsed = append(collapsed, "")
				blank = true
			}
			continue
		}
		blank = false
		if havePrev && line == prev {
			run++
		} else {
			flush()
			prev, havePrev, run = line, true, 1
		}
	}
	flush()

	if len(collapsed) > cfg.TextHead+cfg.TextTail+10 {
		elided := len(collapsed) - cfg.TextHead - cfg.TextTail
		var out []string
		out = append(out, collapsed[:cfg.TextHead]...)
		out = append(out, fmt.Sprintf("…⟨%d lines elided⟩", elided))
		out = append(out, collapsed[len(collapsed)-cfg.TextTail:]...)
		return strings.Join(out, "\n")
	}
	return strings.Join(collapsed, "\n")
}
