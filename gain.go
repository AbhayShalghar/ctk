package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type statRec struct {
	Ts    string `json:"ts"`
	Tool  string `json:"tool"`
	Kind  string `json:"kind"`
	InTok int    `json:"inTok"`
	Saved int    `json:"saved"`
	proj  string // populated in --global mode (the project dir owning the stats)
}

func cmdGain(args []string) {
	history, reset, global := false, false, false
	for _, a := range args {
		switch a {
		case "--history":
			history = true
		case "--reset":
			reset = true
		case "--global", "-g":
			global = true
		}
	}

	// Gather records (+ the stats files they came from, for --reset).
	var recs []statRec
	var files []string
	if global {
		home, _ := os.UserHomeDir()
		files = findStatsFiles(home)
		for _, f := range files {
			proj := filepath.Dir(filepath.Dir(f)) // parent of .ctk
			for _, r := range loadStats(f) {
				r.proj = proj
				recs = append(recs, r)
			}
		}
	} else {
		cwd, _ := os.Getwd()
		f := filepath.Join(cwd, ".ctk", "stats.jsonl")
		files = []string{f}
		recs = loadStats(f)
	}

	if reset {
		for _, f := range files {
			_ = os.Remove(f)
		}
		fmt.Printf("ctk stats cleared%s.\n", map[bool]string{true: " (all projects)", false: ""}[global])
		return
	}

	if len(recs) == 0 {
		if global {
			fmt.Println("No ctk savings recorded in any project yet. Run some tool calls with the hook active.")
		} else {
			fmt.Println("No ctk savings recorded in this folder yet. Try `ctk gain --global`, or run tool calls here.")
		}
		return
	}

	if history {
		sort.Slice(recs, func(i, j int) bool { return recs[i].Ts < recs[j].Ts })
		renderHistory(recs, global)
		return
	}

	fmt.Println(map[bool]string{true: "ctk gain (all projects)", false: "ctk gain"}[global])
	fmt.Println()
	renderTotals(recs)
	printTable("by tool", groupBy(recs, func(r statRec) string { return r.Tool }))
	printTable("by content kind", groupBy(recs, func(r statRec) string { return r.Kind }))
	if global {
		home, _ := os.UserHomeDir()
		printTable("by project", groupBy(recs, func(r statRec) string { return shortenPath(r.proj, home) }))
	}
	hint := "(run with --history for recent calls, --reset to clear"
	if !global {
		hint += ", --global for all projects"
	}
	fmt.Println("\n" + hint + ")")
}

func renderTotals(recs []statRec) {
	var inTok, saved int
	for _, r := range recs {
		inTok += r.InTok
		saved += r.Saved
	}
	pct := 0
	if inTok > 0 {
		pct = int(float64(saved)/float64(inTok)*100 + 0.5)
	}
	fmt.Printf("  tokens in        %s\n", comma(inTok))
	fmt.Printf("  tokens out       %s\n", comma(inTok-saved))
	fmt.Printf("  tokens saved     %s  (%d%% reduction)\n", comma(saved), pct)
	fmt.Printf("  compressions     %s\n", comma(len(recs)))
}

func renderHistory(recs []statRec, global bool) {
	n := 20
	if len(recs) < n {
		n = len(recs)
	}
	fmt.Printf("ctk — last %d compressions%s\n\n", n, map[bool]string{true: " (all projects)", false: ""}[global])
	fmt.Printf("%-21s%-32s%-10s%9s\n", "when", "tool", "kind", "saved")
	fmt.Println(strings.Repeat("-", 72))
	var saved int
	for _, r := range recs {
		saved += r.Saved
	}
	for _, r := range recs[len(recs)-n:] {
		when := strings.Replace(r.Ts, "T", " ", 1)
		if len(when) > 19 {
			when = when[:19]
		}
		fmt.Printf("%-21s%-32s%-10s%9s\n", when, trunc(r.Tool, 30), trunc(r.Kind, 9), comma(r.Saved))
	}
	fmt.Println(strings.Repeat("-", 72))
	fmt.Printf("Total saved: %s tokens across %s calls\n", comma(saved), comma(len(recs)))
}

// findStatsFiles walks root for every .ctk/stats.jsonl, skipping heavy dirs.
func findStatsFiles(root string) []string {
	skip := map[string]bool{
		"node_modules": true, "Library": true, ".git": true, ".cache": true,
		"Caches": true, ".Trash": true, ".gradle": true, ".m2": true,
		"vendor": true, "dist": true, "go": true, ".npm": true,
		".rustup": true, ".cargo": true, "Applications": true,
	}
	var out []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		if d.Name() == ".ctk" {
			sf := filepath.Join(p, "stats.jsonl")
			if _, e := os.Stat(sf); e == nil {
				out = append(out, sf)
			}
			return filepath.SkipDir // don't descend into .ctk
		}
		if skip[d.Name()] {
			return filepath.SkipDir
		}
		return nil
	})
	return out
}

func shortenPath(p, home string) string {
	if home != "" && strings.HasPrefix(p, home) {
		return "~" + p[len(home):]
	}
	return p
}

func loadStats(path string) []statRec {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []statRec
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var r statRec
		if json.Unmarshal([]byte(line), &r) == nil {
			out = append(out, r)
		}
	}
	return out
}

type group struct {
	name  string
	calls int
	saved int
}

func groupBy(recs []statRec, key func(statRec) string) []group {
	m := map[string]*group{}
	for _, r := range recs {
		k := key(r)
		if m[k] == nil {
			m[k] = &group{name: k}
		}
		m[k].calls++
		m[k].saved += r.Saved
	}
	out := make([]group, 0, len(m))
	for _, g := range m {
		out = append(out, *g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].saved > out[j].saved })
	return out
}

func printTable(title string, gs []group) {
	fmt.Printf("\n%s\n", title)
	fmt.Printf("  %-40s%7s%11s\n", "name", "calls", "saved")
	fmt.Printf("  %s\n", strings.Repeat("-", 58))
	for _, g := range gs {
		fmt.Printf("  %-40s%7s%11s\n", trunc(g.name, 38), comma(g.calls), comma(g.saved))
	}
}

func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// comma formats an int with thousands separators (no external deps).
func comma(n int) string {
	s := fmt.Sprintf("%d", n)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	var parts []string
	for len(s) > 3 {
		parts = append([]string{s[len(s)-3:]}, parts...)
		s = s[:len(s)-3]
	}
	parts = append([]string{s}, parts...)
	res := strings.Join(parts, ",")
	if neg {
		res = "-" + res
	}
	return res
}
