package main

import (
	"bufio"
	"encoding/json"
	"fmt"
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
}

func cmdGain(args []string) {
	history, reset := false, false
	for _, a := range args {
		switch a {
		case "--history":
			history = true
		case "--reset":
			reset = true
		}
	}
	cwd, _ := os.Getwd()
	statsPath := filepath.Join(cwd, ".ctk", "stats.jsonl")

	if reset {
		_ = os.Remove(statsPath)
		fmt.Println("ctk stats cleared.")
		return
	}

	recs := loadStats(statsPath)
	if len(recs) == 0 {
		fmt.Println("No ctk savings recorded yet. Run some tool calls with the hook active.")
		return
	}

	var inTok, saved int
	for _, r := range recs {
		inTok += r.InTok
		saved += r.Saved
	}
	outTok := inTok - saved
	pct := 0
	if inTok > 0 {
		pct = int(float64(saved)/float64(inTok)*100 + 0.5)
	}

	if history {
		n := 20
		if len(recs) < n {
			n = len(recs)
		}
		fmt.Printf("ctk — last %d compressions\n\n", n)
		fmt.Printf("%-22s%-34s%-12s%9s\n", "when", "tool", "kind", "saved")
		fmt.Println(strings.Repeat("-", 77))
		for _, r := range recs[len(recs)-n:] {
			when := strings.Replace(r.Ts, "T", " ", 1)
			if len(when) > 19 {
				when = when[:19]
			}
			fmt.Printf("%-22s%-34s%-12s%9s\n", when, trunc(r.Tool, 32), r.Kind, comma(r.Saved))
		}
		fmt.Println(strings.Repeat("-", 77))
		fmt.Printf("Total saved: %s tokens across %s calls\n", comma(saved), comma(len(recs)))
		return
	}

	fmt.Println("ctk gain")
	fmt.Println()
	fmt.Printf("  tokens in        %s\n", comma(inTok))
	fmt.Printf("  tokens out       %s\n", comma(outTok))
	fmt.Printf("  tokens saved     %s  (%d%% reduction)\n", comma(saved), pct)
	fmt.Printf("  compressions     %s\n", comma(len(recs)))

	printTable("by tool", groupBy(recs, func(r statRec) string { return r.Tool }))
	printTable("by content kind", groupBy(recs, func(r statRec) string { return r.Kind }))
	fmt.Println("\n(run with --history for recent calls, --reset to clear)")
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
	fmt.Printf("  %-36s%7s%11s\n", "name", "calls", "saved")
	fmt.Printf("  %s\n", strings.Repeat("-", 54))
	for _, g := range gs {
		fmt.Printf("  %-36s%7s%11s\n", trunc(g.name, 34), comma(g.calls), comma(g.saved))
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
