package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ---- aggregation ------------------------------------------------------------

type modelAgg struct{ in, out, cacheRead, cacheCreate, turns int }

type report struct {
	Sessions         int                `json:"sessions"`
	Turns            int                `json:"turns"`
	Input            int                `json:"inputTokens"`
	Output           int                `json:"outputTokens"`
	CacheRead        int                `json:"cacheReadTokens"`
	CacheCreate      int                `json:"cacheCreateTokens"`
	CacheHitPct      float64            `json:"cacheHitPct"`
	OutInPct         float64            `json:"outputSharePct"`
	SidechainPct     float64            `json:"subagentSharePct"`
	Cost             float64            `json:"costUSD"`
	CostByType       map[string]float64 `json:"costUSDByType"`
	CostByProject    map[string]float64 `json:"costUSDByProject"`
	Models           map[string]int     `json:"turnsByModel"`
	ToolTokens       map[string]int     `json:"toolResultTokensEst"`
	LongSessions     int                `json:"sessionsOver400K"`
	Top5Pct          float64            `json:"top5SessionsSpendPct"`
	CapSavePct       float64            `json:"capContextSavePct"`
	CapSaveUSD       float64            `json:"capContextSaveUSD"`
	UnpricedTurns    int                `json:"unpricedTurns"`
	SubTopTierPct    float64            `json:"subagentTopTierTurnPct"`
	SubagentRuns     int                `json:"subagentRuns"`
	SubagentUSD      float64            `json:"subagentCostUSD"`
	ToolCarryUSD     map[string]float64 `json:"toolCarriedCostUSD"`
	RereadUSD        float64            `json:"rereadCostUSD"`
	TopTierSwitchUSD float64            `json:"topTierToSonnetSavingUSD"`
	SubSwitchUSD     float64            `json:"subagentToSonnetSavingUSD"`
	RereadTokens     int                `json:"rereadTokensEst"`
	TopSessions      []sessionStat      `json:"costliestSessions"`
	RepeatReads      []repeatRead       `json:"repeatedReads"`
	Opportunities    []opportunity      `json:"opportunities"`
}

type opportunity struct {
	Impact     string  `json:"impact"`
	Title      string  `json:"title"`
	Detail     string  `json:"detail"`
	SaveLowUSD float64 `json:"estSaveLowUSD,omitempty"`
	SaveUSD    float64 `json:"estSaveHighUSD,omitempty"`
	Tokens     int     `json:"tokens,omitempty"`
}

type sessionStat struct {
	ID       string  `json:"session"`
	Proj     string  `json:"project"`
	Turns    int     `json:"turns"`
	PeakCtx  int     `json:"peakContext"`
	AvgCtx   int     `json:"avgContext"`
	HitPct   float64 `json:"cacheHitPct"`
	Cost     float64 `json:"costUSD"`
	SharePct float64 `json:"spendSharePct"`
	SubUSD   float64 `json:"subagentCostUSD,omitempty"`
	cr, all  int
}

type repeatRead struct {
	File   string `json:"file"`
	Count  int    `json:"count"`
	Sess   string `json:"session"`
	Tokens int    `json:"wastedTokensEst"`
}

func pct100(a, b float64) float64 {
	if b <= 0 {
		return 0
	}
	return 100 * a / b
}

func pct(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return 100 * float64(a) / float64(b)
}

const ctxCap = 200_000 // context size we recommend compacting at

// carried estimates what one tool result costs over the rest of the session:
// one cache write on the turn that first sees it, then a cache read per later turn.
func (s *session) carried(rec resultRec) float64 {
	if rec.at >= len(s.turns) {
		return 0
	}
	pr, _ := priceFor(s.turns[rec.at].model)
	later := len(s.turns) - rec.at - 1
	return float64(rec.tokens) * (pr.CacheWrite + pr.CacheRead*float64(later)) / 1e6
}

func analyze(sessions []*session) report {
	r := report{
		Models: map[string]int{}, ToolTokens: map[string]int{},
		CostByType: map[string]float64{}, CostByProject: map[string]float64{},
		ToolCarryUSD: map[string]float64{},
	}
	var sideTok, allTok int
	var capSave float64
	var all []sessionStat
	var subTurns, subTop int
	subAdd := map[string]float64{} // parent session id -> cost of its subagent runs
	for _, s := range sessions {
		var st sessionStat
		var ctxSum int
		for _, t := range s.turns {
			if strings.HasPrefix(t.model, "<") { // "<synthetic>" placeholder turns
				continue
			}
			u := t.usage
			pr, priced := priceFor(t.model)
			if !priced {
				r.UnpricedTurns++
			}
			r.Turns++
			st.Turns++
			r.Input += u.In
			r.Output += u.Out
			r.CacheRead += u.CacheRead
			r.CacheCreate += u.CacheCreate
			r.CostByType["Cache read"] += float64(u.CacheRead) * pr.CacheRead / 1e6
			r.CostByType["Cache write"] += float64(u.CacheCreate) * pr.CacheWrite / 1e6
			r.CostByType["Fresh input"] += float64(u.In) * pr.In / 1e6
			r.CostByType["Output"] += float64(u.Out) * pr.Out / 1e6
			c := u.ctx()
			ctxSum += c
			if c > st.PeakCtx {
				st.PeakCtx = c
			}
			if c > ctxCap {
				capSave += float64(c-ctxCap) * pr.CacheRead / 1e6
			}
			st.Cost += pr.cost(u)
			if son, ok := priceFor("claude-sonnet-5-5"); ok && priced && (strings.Contains(t.model, "opus") || strings.Contains(t.model, "fable")) {
				if s.sub {
					r.SubSwitchUSD += pr.cost(u) - son.cost(u)
				} else {
					r.TopTierSwitchUSD += pr.cost(u) - son.cost(u)
				}
			}
			st.cr += u.CacheRead
			st.all += c
			r.Models[t.model]++
			if s.sub {
				subTurns++
				if strings.Contains(t.model, "opus") || strings.Contains(t.model, "fable") {
					subTop++
				}
			}
			tok := u.In + u.Out + u.CacheRead + u.CacheCreate
			allTok += tok
			if t.sidechain {
				sideTok += tok
			}
		}
		if st.Turns == 0 {
			continue
		}
		r.Cost += st.Cost
		r.CostByProject[s.proj] += st.Cost
		if s.sub {
			// own context, so keep it out of the per-session context stats
			r.SubagentRuns++
			r.SubagentUSD += st.Cost
			subAdd[s.parent] += st.Cost
		} else {
			r.Sessions++
			st.ID, st.Proj = s.id, s.proj
			st.AvgCtx = ctxSum / st.Turns
			st.HitPct = pct(st.cr, st.all)
			if st.PeakCtx > 400_000 {
				r.LongSessions++
			}
			all = append(all, st)
		}

		for name, ch := range s.toolChars {
			r.ToolTokens[name] += ch / 4
		}
		seen := map[string]int{}
		waste := map[string]int{}
		for _, rec := range s.results {
			c := s.carried(rec)
			r.ToolCarryUSD[rec.tool] += c
			if rec.file != "" && s.reads[rec.file] >= 3 {
				if seen[rec.file]++; seen[rec.file] > 1 { // first read is useful, repeats are waste
					waste[rec.file] += rec.tokens
					r.RereadTokens += rec.tokens
					r.RereadUSD += c
				}
			}
		}
		for file, tok := range waste {
			r.RepeatReads = append(r.RepeatReads, repeatRead{File: file, Count: s.reads[file], Sess: s.id, Tokens: tok})
		}
	}
	r.SubTopTierPct = pct(subTop, subTurns)
	r.CacheHitPct = pct(r.CacheRead, r.Input+r.CacheRead+r.CacheCreate)
	r.OutInPct = pct(r.Output, r.Input+r.CacheRead+r.CacheCreate+r.Output)
	r.SidechainPct = pct(sideTok, allTok)
	if r.Cost > 0 {
		r.CapSavePct = 100 * capSave / r.Cost
		r.CapSaveUSD = capSave
	}
	for i := range all {
		all[i].SubUSD = subAdd[all[i].ID]
		all[i].Cost += all[i].SubUSD
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Cost > all[j].Cost })
	for i := range all {
		all[i].SharePct = 100 * all[i].Cost / max(r.Cost, 1e-9)
		if i < 5 {
			r.Top5Pct += all[i].SharePct
		}
	}
	if len(all) > 5 {
		all = all[:5]
	}
	r.TopSessions = all
	sort.Slice(r.RepeatReads, func(i, j int) bool { return r.RepeatReads[i].Tokens > r.RepeatReads[j].Tokens })
	if len(r.RepeatReads) > 5 {
		r.RepeatReads = r.RepeatReads[:5]
	}
	r.Opportunities = opportunities(r)
	return r
}

func toolAdvice(t string) string {
	switch {
	case t == "Bash":
		return "Pipe noisy commands through head/tail/grep, or let ctk/rtk compress shell output."
	case t == "Read":
		return "Read with offset/limit instead of whole files, and don't re-read unchanged files."
	case strings.Contains(t, "opensearch") || strings.Contains(strings.ToLower(t), "search"):
		return "Cap `size` and use _source filtering or aggregations, so searches return a few fields instead of whole documents."
	case strings.HasPrefix(t, "mcp__"):
		return "Ask this MCP tool for fewer fields or smaller pages; ctk compresses its output too."
	}
	return "Trim this tool's output where you can."
}

// opportunities turns the aggregates into dollar-estimated savings. Every
// estimate is a range built from a stated assumption, not a measurement, and
// the ranges overlap (trimming tool output also shrinks context), so the list is
// ranked by midpoint but must not be summed.
func opportunities(r report) []opportunity {
	var out []opportunity
	if r.Sessions == 0 || r.Cost <= 0 {
		return out
	}
	add := func(o opportunity) {
		mid := (o.SaveLowUSD + o.SaveUSD) / 2
		share := 100 * mid / r.Cost
		if share < 1 {
			return // not worth a line
		}
		o.Impact = "low"
		if share >= 10 {
			o.Impact = "high"
		} else if share >= 3 {
			o.Impact = "medium"
		}
		out = append(out, o)
	}

	add(opportunity{
		Title: "Keep sessions under ~200K context", SaveLowUSD: r.CapSaveUSD * 0.5, SaveUSD: r.CapSaveUSD,
		Detail: fmt.Sprintf("%d of %d sessions grew past 400K and your 5 costliest are %.0f%% of spend: every turn re-reads the whole context. /compact near 200K, or /clear between unrelated tasks. Assumes between half and all of the context above 200K could be dropped.",
			r.LongSessions, r.Sessions, r.Top5Pct),
	})

	if r.RereadUSD > 0 && len(r.RepeatReads) > 0 {
		x := r.RepeatReads[0]
		add(opportunity{
			Title: "Stop re-reading the same files", SaveLowUSD: r.RereadUSD * 0.5, SaveUSD: r.RereadUSD,
			Detail: fmt.Sprintf("~%s tokens were loaded again by repeat reads and then carried through later turns. Worst: %s (%d reads in one session). Some re-reads are needed after the file changed, so this assumes half to all were avoidable.",
				compact(r.RereadTokens), filepath.Base(x.File), x.Count),
		})
	}

	type kv struct {
		k string
		v float64
	}
	var tools []kv
	for k, v := range r.ToolCarryUSD {
		tools = append(tools, kv{k, v})
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].v > tools[j].v })
	sumTok := 0
	for _, v := range r.ToolTokens {
		sumTok += v
	}
	for i := 0; i < len(tools) && i < 4; i++ {
		t := tools[i].k
		add(opportunity{
			Title: fmt.Sprintf("%s output is heavy", toolLabel(t)), SaveLowUSD: tools[i].v * 0.25, SaveUSD: tools[i].v * 0.5,
			Detail: fmt.Sprintf("~%s tokens (%.0f%% of tool output). Once returned, each result is re-read on every later turn, which costs %s in total. %s Assumes trimming a quarter to half of it.",
				compact(r.ToolTokens[t]), pct(r.ToolTokens[t], sumTok), money(tools[i].v), toolAdvice(t)),
		})
	}

	var heavy int
	for m, n := range r.Models {
		if strings.Contains(m, "opus") || strings.Contains(m, "fable") {
			heavy += n
		}
	}
	if r.TopTierSwitchUSD > 0 {
		add(opportunity{
			Title: "Move routine turns off the top-tier model", SaveLowUSD: r.TopTierSwitchUSD * 0.10, SaveUSD: r.TopTierSwitchUSD * 0.25,
			Detail: fmt.Sprintf("%.0f%% of turns ran on Opus/Fable. Pricing those turns at Sonnet 5.5 would save %s; this assumes 10-25%% of them (edits, greps, summaries) are routine. Switch at the start of a session, because prompt caches don't carry across models.",
				pct(heavy, r.Turns), money(r.TopTierSwitchUSD)),
		})
	}

	if r.SubSwitchUSD > 0 {
		add(opportunity{
			Title: "Run subagents on a cheaper model", SaveLowUSD: r.SubSwitchUSD * 0.3, SaveUSD: r.SubSwitchUSD * 0.6,
			Detail: fmt.Sprintf("Subagents cost %s (%.0f%% of spend) and %.0f%% of their turns ran on Opus/Fable. They mostly search and read, so Sonnet 5.5 or Haiku usually suffices: full price difference %s; this assumes 30-60%% of that is safe to take. Set the model in the subagent's definition.",
				money(r.SubagentUSD), pct100(r.SubagentUSD, r.Cost), r.SubTopTierPct, money(r.SubSwitchUSD)),
		})
	}

	if r.CacheHitPct < 80 {
		out = append(out, opportunity{
			Impact: "high", Title: "Cache hit rate is low",
			Detail: fmt.Sprintf("%.0f%% (target 80%%+). Avoid editing CLAUDE.md, MCP servers or tool lists mid-session; they invalidate the cached prefix. Not dollar-estimated.", r.CacheHitPct),
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].SaveLowUSD+out[i].SaveUSD > out[j].SaveLowUSD+out[j].SaveUSD
	})
	return out
}

func homeDir() string { h, _ := os.UserHomeDir(); return h }

// ---- command ----------------------------------------------------------------

const analyzeUsage = `usage: ctk analyze [--global] [--days N] [--json]

  --global   all projects under ~/.claude/projects (default: current folder's project)
  --days N   only transcripts modified in the last N days
  --json     machine-readable output
`

func cmdAnalyze(args []string) {
	global, asJSON := false, false
	var since time.Time
	fail := func(msg string) {
		fmt.Fprintln(os.Stderr, "ctk analyze: "+msg)
		fmt.Fprint(os.Stderr, analyzeUsage)
		os.Exit(2)
	}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--global", "-g":
			global = true
		case "--json":
			asJSON = true
		case "--days":
			d := 0
			if i+1 >= len(args) {
				fail("--days needs a number")
			}
			if _, err := fmt.Sscanf(args[i+1], "%d", &d); err != nil || d <= 0 {
				fail("--days needs a positive number, got " + args[i+1])
			}
			since = time.Now().AddDate(0, 0, -d)
			i++
		case "--help", "-h":
			fmt.Print(analyzeUsage)
			return
		default:
			fail("unknown option " + args[i])
		}
	}
	files, projs := transcriptFiles(global, since)
	if len(files) == 0 {
		fmt.Println("no Claude Code transcripts found for this scope (try --global)")
		return
	}
	var sessions []*session
	for i, f := range files {
		if s := parseSession(f, projs[i]); s != nil {
			sessions = append(sessions, s)
		}
	}
	r := analyze(sessions)
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(r)
		return
	}
	renderReport(r, global)
}
