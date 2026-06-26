package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
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

// ---- presentation ---------------------------------------------------------

var useColor = func() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	fi, err := os.Stdout.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}()

const (
	cReset  = "\033[0m"
	cBold   = "\033[1m"
	cGreen  = "\033[32m"
	cYellow = "\033[33m"
	cRed    = "\033[31m"
	cCyan   = "\033[36m"
	cGray   = "\033[90m"
)

func paint(s, code string) string {
	if !useColor || code == "" {
		return s
	}
	return code + s + cReset
}

func humanize(n int) string {
	f := float64(n)
	switch {
	case f >= 1e6:
		return fmt.Sprintf("%.1fM", f/1e6)
	case f >= 1e3:
		return fmt.Sprintf("%.1fK", f/1e3)
	default:
		return strconv.Itoa(n)
	}
}

func pctColor(p int) string {
	switch {
	case p >= 40:
		return cGreen
	case p >= 20:
		return cYellow
	default:
		return cRed
	}
}

func ljust(s string, w int) string {
	if len(s) >= w {
		return s
	}
	return s + strings.Repeat(" ", w-len(s))
}
func rjust(s string, w int) string {
	if len(s) >= w {
		return s
	}
	return strings.Repeat(" ", w-len(s)) + s
}

func cell(s string, w int, right bool, color string) string {
	if right {
		s = rjust(s, w)
	} else {
		s = ljust(s, w)
	}
	return paint(s, color)
}

// ---- command --------------------------------------------------------------

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

	var recs []statRec
	var files []string
	if global {
		home, _ := os.UserHomeDir()
		files = findStatsFiles(home)
		for _, f := range files {
			proj := filepath.Dir(filepath.Dir(f))
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
		scope := ""
		if global {
			scope = " (all projects)"
		}
		fmt.Printf("ctk stats cleared%s.\n", scope)
		return
	}

	if len(recs) == 0 {
		if global {
			fmt.Println(paint("No ctk savings recorded in any project yet.", cYellow))
		} else {
			fmt.Println(paint("No ctk savings in this folder yet.", cYellow) + " Try " + paint("ctk gain --global", cBold) + ".")
		}
		return
	}

	if history {
		sort.Slice(recs, func(i, j int) bool { return recs[i].Ts < recs[j].Ts })
		renderHistory(recs, global)
		return
	}

	scope := ""
	if global {
		scope = " (all projects)"
	}

	var inTok, saved int
	for _, r := range recs {
		inTok += r.InTok
		saved += r.Saved
	}
	pct := 0
	if inTok > 0 {
		pct = int(float64(saved)/float64(inTok)*100 + 0.5)
	}

	fmt.Println()
	fmt.Println(paint("ctk · Token Savings"+scope, cBold+cGreen))
	fmt.Println(paint(strings.Repeat("═", 52), cGray))
	fmt.Println()
	fmt.Printf("  %s %s\n", ljust("Compressions", 16), paint(comma(len(recs)), cBold))
	fmt.Printf("  %s %s\n", ljust("Tokens in", 16), humanize(inTok))
	fmt.Printf("  %s %s\n", ljust("Tokens out", 16), humanize(inTok-saved))
	fmt.Printf("  %s %s  %s\n", ljust("Tokens saved", 16),
		paint(humanize(saved), cBold+cGreen), paint(fmt.Sprintf("(%d%% saved)", pct), pctColor(pct)))

	filled := pct * 24 / 100
	meter := paint(strings.Repeat("█", filled), pctColor(pct)) + paint(strings.Repeat("░", 24-filled), cGray)
	fmt.Printf("  %s %s %s\n", ljust("Efficiency", 16), meter, paint(fmt.Sprintf("%d%%", pct), cBold+pctColor(pct)))

	renderTable("By tool", groupBy(recs, func(r statRec) string { return r.Tool }))
	renderTable("By content kind", groupBy(recs, func(r statRec) string { return r.Kind }))
	if global {
		home, _ := os.UserHomeDir()
		renderTable("By project", groupBy(recs, func(r statRec) string { return shortenPath(r.proj, home) }))
	}

	hint := "--history for recent calls · --reset to clear"
	if !global {
		hint += " · --global for all projects"
	}
	fmt.Println()
	fmt.Println(paint("  "+hint, cGray))
}

func renderTable(title string, gs []group) {
	maxSaved := 0
	for _, g := range gs {
		if g.saved > maxSaved {
			maxSaved = g.saved
		}
	}
	fmt.Println()
	fmt.Println(paint(title, cBold+cCyan))
	// header
	fmt.Println("  " + cell("#", 3, true, cGray) + " " + cell("name", 38, false, cGray) +
		" " + cell("calls", 6, true, cGray) + " " + cell("saved", 8, true, cGray) +
		" " + cell("cut", 5, true, cGray) + "  " + paint("impact", cGray))
	for i, g := range gs {
		red := 0
		if g.inTok > 0 {
			red = int(float64(g.saved)/float64(g.inTok)*100 + 0.5)
		}
		barLen := 0
		if maxSaved > 0 {
			barLen = g.saved * 14 / maxSaved
		}
		impact := paint(strings.Repeat("▇", barLen), pctColor(red))
		fmt.Println("  " +
			cell(strconv.Itoa(i+1)+".", 3, true, cGray) + " " +
			cell(trunc(g.name, 38), 38, false, "") + " " +
			cell(comma(g.calls), 6, true, "") + " " +
			cell(humanize(g.saved), 8, true, cBold) + " " +
			cell(fmt.Sprintf("%d%%", red), 5, true, pctColor(red)) + "  " +
			impact)
	}
}

func renderHistory(recs []statRec, global bool) {
	n := 20
	if len(recs) < n {
		n = len(recs)
	}
	scope := ""
	if global {
		scope = " (all projects)"
	}
	var saved int
	for _, r := range recs {
		saved += r.Saved
	}
	fmt.Println()
	fmt.Println(paint(fmt.Sprintf("ctk · last %d compressions%s", n, scope), cBold+cGreen))
	fmt.Println(paint(strings.Repeat("═", 60), cGray))
	fmt.Println("  " + cell("when", 19, false, cGray) + " " + cell("tool", 30, false, cGray) +
		" " + cell("kind", 6, false, cGray) + " " + cell("saved", 8, true, cGray))
	for _, r := range recs[len(recs)-n:] {
		when := strings.Replace(r.Ts, "T", " ", 1)
		if len(when) > 19 {
			when = when[:19]
		}
		fmt.Println("  " + cell(when, 19, false, cGray) + " " +
			cell(trunc(r.Tool, 30), 30, false, "") + " " +
			cell(trunc(r.Kind, 6), 6, false, "") + " " +
			cell(humanize(r.Saved), 8, true, cBold+cGreen))
	}
	fmt.Println(paint(strings.Repeat("─", 60), cGray))
	fmt.Printf("  total: %s tokens across %s calls\n",
		paint(humanize(saved), cBold+cGreen), comma(len(recs)))
}

// ---- data -----------------------------------------------------------------

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
			return filepath.SkipDir
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
	inTok int
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
		m[k].inTok += r.InTok
	}
	out := make([]group, 0, len(m))
	for _, g := range m {
		out = append(out, *g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].saved > out[j].saved })
	return out
}

func trunc(s string, n int) string {
	if len(s) > n {
		if n > 1 {
			return s[:n-1] + "…"
		}
		return s[:n]
	}
	return s
}

// comma formats an int with thousands separators (no external deps).
func comma(n int) string {
	s := strconv.Itoa(n)
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
