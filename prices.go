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
	In         float64 `json:"input"`
	Out        float64 `json:"output"`
	CacheWrite float64 `json:"cacheWrite"`
	CacheRead  float64 `json:"cacheRead"`
}

// List API prices (Anthropic first-party, cached 2026-09-25). Input/output come
// from the published model table. Cache read for Opus 5.5 and Sonnet 5.5 is the
// published $0.20. Everywhere else the model docs don't list cache prices, so we
// assume the standard multipliers: write = 1.25x input, read = 0.1x input.
// Override or extend with ~/.config/ctk/prices.json (same shape, keyed by model id).
var defaultPrices = map[string]price{
	"claude-fable-5-1":  {10, 50, 12.5, 1},
	"claude-fable-5":    {10, 50, 12.5, 0.25},
	"claude-opus-5-5":   {4, 20, 5, 0.20},
	"claude-opus-5":     {5, 25, 6.25, 0.5},
	"claude-opus-4-8":   {5, 25, 6.25, 0.5},
	"claude-opus-4-7":   {5, 25, 6.25, 0.5},
	"claude-opus-4-6":   {5, 25, 6.25, 0.5},
	"claude-sonnet-5-5": {2, 10, 2.5, 0.20},
	"claude-sonnet-5":   {2, 10, 2.5, 0.2},
	"claude-sonnet-4-6": {3, 15, 3.75, 0.3},
	"claude-haiku-4-5":  {1, 5, 1.25, 0.1},
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

func (p price) cost(u usageBlock) float64 {
	return (float64(u.In)*p.In + float64(u.CacheCreate)*p.CacheWrite +
		float64(u.CacheRead)*p.CacheRead + float64(u.Out)*p.Out) / 1e6
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
