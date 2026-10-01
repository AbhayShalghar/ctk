package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// price is USD per million tokens.
type price struct {
	In          float64 `json:"input"`
	Out         float64 `json:"output"`
	CacheWrite  float64 `json:"cacheWrite"`   // 5-minute cache write
	CacheWrite1 float64 `json:"cacheWrite1h"` // 1-hour cache write
	CacheRead   float64 `json:"cacheRead"`
}

// List API prices, copied from https://platform.claude.com/docs/en/about-claude/pricing
// (checked 2026-10-01). Cache write is 1.25x input (5-minute) or 2x (1-hour);
// cache read is 0.1x, except Opus 5.5 (0.05x) and Fable/Mythos 5.1 (0.025x).
// Prices change: override or extend with ~/.config/ctk/prices.json (same shape,
// keyed by model id).
var defaultPrices = map[string]price{
	"claude-fable-5-1":  {10, 50, 12.5, 20, 0.25},
	"claude-fable-5":    {10, 50, 12.5, 20, 1},
	"claude-opus-5-5":   {4, 20, 5, 8, 0.20},
	"claude-opus-5":     {5, 25, 6.25, 10, 0.50},
	"claude-opus-4-8":   {5, 25, 6.25, 10, 0.50},
	"claude-opus-4-7":   {5, 25, 6.25, 10, 0.50},
	"claude-opus-4-6":   {5, 25, 6.25, 10, 0.50},
	"claude-opus-4-5":   {5, 25, 6.25, 10, 0.50},
	"claude-sonnet-5-5": {2, 10, 2.5, 4, 0.20},
	"claude-sonnet-5":   {2, 10, 2.5, 4, 0.20},
	"claude-sonnet-4-6": {3, 15, 3.75, 6, 0.30},
	"claude-sonnet-4-5": {3, 15, 3.75, 6, 0.30},
	"claude-haiku-4-5":  {1, 5, 1.25, 2, 0.10},
}

var (
	priceTable = loadPrices()
	dateSuffix = regexp.MustCompile(`-\d{8}$`)
)

func loadPrices() map[string]price {
	t := map[string]price{}
	for k, v := range defaultPrices {
		t[k] = v
	}
	home, _ := os.UserHomeDir()
	if b, err := os.ReadFile(filepath.Join(home, ".config", "ctk", "prices.json")); err == nil {
		var over map[string]price
		if err := json.Unmarshal(b, &over); err != nil {
			fmt.Fprintln(os.Stderr, "ctk: ignoring ~/.config/ctk/prices.json:", err)
		}
		for k, v := range over {
			t[k] = v
		}
	}
	return t
}

func priceFor(model string) (price, bool) {
	p, ok := priceTable[dateSuffix.ReplaceAllString(model, "")]
	return p, ok
}

// writeCost is the cache-write cost of one turn, in USD.
func (p price) writeCost(u usageBlock) float64 {
	m5, h1 := u.writeTokens()
	return (float64(m5)*p.CacheWrite + float64(h1)*p.writeRate1h()) / 1e6
}

// writeRate1h falls back to 2x input when a user override omits cacheWrite1h.
func (p price) writeRate1h() float64 {
	if p.CacheWrite1 > 0 {
		return p.CacheWrite1
	}
	return 2 * p.In
}

func (p price) cost(u usageBlock) float64 {
	return (float64(u.In)*p.In+float64(u.CacheRead)*p.CacheRead+float64(u.Out)*p.Out)/1e6 + p.writeCost(u)
}

func money(v float64) string {
	switch {
	case v >= 10:
		return "$" + comma(int(v+0.5))
	case v >= 1:
		return fmt.Sprintf("$%.2f", v)
	}
	return fmt.Sprintf("$%.3f", v)
}
