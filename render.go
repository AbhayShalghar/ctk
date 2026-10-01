package main

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// ---- rendering --------------------------------------------------------------

const boxW = 74

func vlen(s string) int { return utf8.RuneCountInString(s) }

// compact formats big token counts: 1.2K, 3.4M, 2.7B.
func compact(n int) string {
	f := float64(n)
	switch {
	case f >= 1e9:
		return fmt.Sprintf("%.1fB", f/1e9)
	case f >= 1e6:
		return fmt.Sprintf("%.1fM", f/1e6)
	case f >= 1e3:
		return fmt.Sprintf("%.1fK", f/1e3)
	}
	return fmt.Sprint(n)
}

// bar draws a w-wide gauge for frac in [0,1].
func bar(frac float64, w int, color string) string {
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	n := int(frac*float64(w) + 0.5)
	return paint(strings.Repeat("█", n), color) + paint(strings.Repeat("░", w-n), cGray)
}

func heading(s string) {
	fmt.Println("\n" + paint(" "+strings.ToUpper(s), cBold+cCyan))
	fmt.Println(paint(" "+strings.Repeat("─", boxW-2), cGray))
}

func prettyModel(m string) string {
	parts := strings.Split(strings.TrimPrefix(m, "claude-"), "-")
	var ver []string
	for _, p := range parts[1:] {
		if len(p) == 8 { // trailing date stamp
			continue
		}
		ver = append(ver, p)
	}
	name := strings.ToUpper(parts[0][:1]) + parts[0][1:]
	if len(ver) > 0 {
		name += " " + strings.Join(ver, ".")
	}
	return name
}

func toolLabel(t string) string {
	if !strings.HasPrefix(t, "mcp__") {
		return t
	}
	p := strings.SplitN(strings.TrimPrefix(t, "mcp__"), "__", 2)
	srv := strings.ReplaceAll(strings.TrimPrefix(p[0], "claude_ai_"), "_", " ")
	if len(p) == 2 {
		return srv + " › " + p[1]
	}
	return srv
}

// reverseDomain matches a leading com-<org>- style prefix of Java-style project names.
var reverseDomain = regexp.MustCompile(`^(com|org|io|net)-[a-z0-9]+-`)

func projLabel(dir string) string {
	d := strings.TrimPrefix(dir, projectDirName(homeDir()))
	d = strings.TrimPrefix(d, "-")
	if d == "" {
		return "~ (home)"
	}
	for _, p := range []string{"GolandProjects-", "IdeaProjects-"} {
		d = strings.TrimPrefix(d, p)
	}
	d = reverseDomain.ReplaceAllString(d, "")
	return d
}

// clip truncates to n runes with an ellipsis; padR pads to n runes.
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func padR(s string, n int) string {
	if v := vlen(s); v < n {
		return s + strings.Repeat(" ", n-v)
	}
	return s
}

// wrap word-wraps s to width w; continuation lines get indent.
func wrap(s string, w int, indent string) string {
	var lines []string
	cur := ""
	for _, word := range strings.Fields(s) {
		if cur != "" && vlen(cur)+1+vlen(word) > w {
			lines = append(lines, cur)
			cur = word
			continue
		}
		if cur != "" {
			cur += " "
		}
		cur += word
	}
	lines = append(lines, cur)
	return strings.Join(lines, "\n"+indent)
}

// grade maps a 0-100 "goodness" score to a colour and verdict.
func grade(score float64) (string, string) {
	switch {
	case score >= 85:
		return cGreen, "excellent"
	case score >= 70:
		return cGreen, "good"
	case score >= 50:
		return cYellow, "fair"
	}
	return cRed, "needs work"
}

func gaugeRow(label string, score float64, value string) {
	col, verdict := grade(score)
	fmt.Printf("  %s %s %s  %s\n", ljust(label, 20), bar(score/100, 20, col), rjust(value, 7), paint(verdict, col))
}

func renderReport(r report, global bool) {
	scope := "this project"
	if global {
		scope = "all projects"
	}
	line := func(s string) string { return "│ " + s + strings.Repeat(" ", boxW-4-vlen(s)) + " │" }
	fmt.Println(paint("╭"+strings.Repeat("─", boxW-2)+"╮", cCyan))
	fmt.Println(paint(line("ctk analyze  ·  "+scope), cBold))
	fmt.Println(paint(line(fmt.Sprintf("%s sessions  ·  %s turns  ·  %s API-equivalent", comma(r.Sessions), comma(r.Turns), money(r.Cost))), cGray))
	if r.SubagentRuns > 0 {
		fmt.Println(paint(line(fmt.Sprintf("incl. %d subagent runs  ·  %s (%.0f%% of spend)", r.SubagentRuns, money(r.SubagentUSD), pct100(r.SubagentUSD, r.Cost))), cGray))
	}
	fmt.Println(paint("╰"+strings.Repeat("─", boxW-2)+"╯", cCyan))

	heading("Health")
	gaugeRow("Cache efficiency", r.CacheHitPct, fmt.Sprintf("%.1f%%", r.CacheHitPct))
	gaugeRow("Context discipline", 100-pct(r.LongSessions, r.Sessions), fmt.Sprintf("%d/%d", r.Sessions-r.LongSessions, r.Sessions))
	fmt.Println(paint("  context discipline = sessions that stayed under 400K tokens", cGray))

	heading("Where you can save")
	if len(r.Opportunities) == 0 {
		fmt.Println("  Nothing stands out. Keep it up.")
	}
	for i, o := range r.Opportunities {
		col := map[string]string{"high": cRed, "medium": cYellow}[o.Impact]
		save := ""
		if o.SaveUSD > 0 {
			save = paint(fmt.Sprintf("saves ~%s–%s  (%.0f–%.0f%%)", money(o.SaveLowUSD), money(o.SaveUSD),
				100*o.SaveLowUSD/r.Cost, 100*o.SaveUSD/r.Cost), cGreen)
		}
		fmt.Printf("  %d %s %s\n", i+1, paint(padR(strings.ToUpper(o.Impact), 6), col), paint(o.Title, cBold))
		if save != "" {
			fmt.Println("           " + save)
		}
		fmt.Println("           " + wrap(o.Detail, boxW-12, "           "))
	}
	if len(r.Opportunities) > 1 {
		fmt.Println(paint("  Ranges rest on the stated assumptions; they overlap, so don't add them up.", cGray))
	}

	heading("Where your spend goes  (API list price)")
	type fkv struct {
		k string
		v float64
	}
	var cs []fkv
	for k, v := range r.CostByType {
		cs = append(cs, fkv{k, v})
	}
	sort.Slice(cs, func(i, j int) bool { return cs[i].v > cs[j].v })
	tokOf := map[string]int{"Cache read": r.CacheRead, "Cache write": r.CacheCreate, "Fresh input": r.Input, "Output": r.Output}
	for _, c := range cs {
		fmt.Printf("  %s %s %9s %5.1f%%  %s\n", ljust(c.k, 12), bar(c.v/max(r.Cost, 1e-9), 20, cCyan), money(c.v), 100*c.v/max(r.Cost, 1e-9),
			paint(compact(tokOf[c.k])+" tokens", cGray))
	}
	fmt.Println(paint(fmt.Sprintf("  total %s at list API prices. Subscription plans (Pro/Max) aren't billed per token,", money(r.Cost)), cGray))
	fmt.Println(paint("  so read this as API-equivalent cost: a measure of relative load, not your invoice.", cGray))
	if r.UnpricedTurns > 0 {
		fmt.Println(paint(fmt.Sprintf("  %d turns use a model with no price entry and count as $0; add it in ~/.config/ctk/prices.json", r.UnpricedTurns), cYellow))
	}

	if global && len(r.CostByProject) > 1 {
		heading("Spend by project")
		var ps []fkv
		for k, v := range r.CostByProject {
			ps = append(ps, fkv{k, v})
		}
		sort.Slice(ps, func(i, j int) bool { return ps[i].v > ps[j].v })
		for i, p := range ps {
			if i == 6 {
				break
			}
			fmt.Printf("  %s %s %9s %5.1f%%\n", padR(clip(projLabel(p.k), 34), 34), bar(p.v/ps[0].v, 20, cCyan), money(p.v), 100*p.v/max(r.Cost, 1e-9))
		}
	}

	heading("Costliest sessions")
	fmt.Println(paint("  session   share of spend              cost   turns  avg ctx  peak ctx", cGray))
	for _, b := range r.TopSessions {
		col := cGreen
		if b.PeakCtx > 400_000 {
			col = cRed
		} else if b.PeakCtx > 200_000 {
			col = cYellow
		}
		where := ""
		if global {
			where = "  " + paint(clip(projLabel(b.Proj), 28), cGray)
		}
		if b.SubUSD > 0 {
			where += paint("  +"+money(b.SubUSD)+" subagents", cGray)
		}
		fmt.Printf("  %s  %s %4.1f%% %9s  %6s  %7s  %s%s\n", shortID(b.ID), bar(b.SharePct/max(r.TopSessions[0].SharePct, 1e-9), 12, cCyan), b.SharePct,
			money(b.Cost), comma(b.Turns), compact(b.AvgCtx), paint(rjust(compact(b.PeakCtx), 8), col), where)
	}
	fmt.Println(paint(fmt.Sprintf("  top 5 = %.0f%% of all spend. Cost ≈ turns × context size.", r.Top5Pct), cGray))

	type kv struct {
		k string
		v int
	}
	var ts []kv
	sum := 0
	for k, v := range r.ToolTokens {
		ts = append(ts, kv{k, v})
		sum += v
	}
	sort.Slice(ts, func(i, j int) bool { return ts[i].v > ts[j].v })
	if len(ts) > 0 {
		heading("Heaviest tool output  (est. tokens)")
		for i, t := range ts {
			if i == 6 {
				break
			}
			fmt.Printf("  %s %s %7s  %5.1f%%\n", padR(clip(toolLabel(t.k), 34), 34), bar(float64(t.v)/float64(ts[0].v), 12, cYellow),
				compact(t.v), pct(t.v, sum))
		}
	}

	heading("Model mix  (turns)")
	var ms []kv
	for k, v := range r.Models {
		ms = append(ms, kv{k, v})
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].v > ms[j].v })
	for _, m := range ms {
		fmt.Printf("  %s %s %5.1f%%  %s\n", ljust(prettyModel(m.k), 12), bar(float64(m.v)/float64(r.Turns), 20, cGreen),
			pct(m.v, r.Turns), paint(comma(m.v)+" turns", cGray))
	}
	fmt.Println()
}

// shortID is the first 8 characters of a session id, safe for short names.
func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
