package main

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---- fixture helpers --------------------------------------------------------

type usg = map[string]int

func assistant(sess, id, model string, u usg, blocks ...map[string]any) map[string]any {
	if blocks == nil {
		blocks = []map[string]any{{"type": "text", "text": "ok"}}
	}
	return map[string]any{
		"type": "assistant", "sessionId": sess, "isSidechain": strings.Contains(sess, "agent"),
		"timestamp": "2026-09-01T10:00:00Z",
		"message":   map[string]any{"id": id, "model": model, "usage": u, "content": blocks},
	}
}

func toolUse(id, name string, input map[string]any) map[string]any {
	return map[string]any{"type": "tool_use", "id": id, "name": name, "input": input}
}

func toolResult(sess, toolID, content string) map[string]any {
	return map[string]any{
		"type": "user", "sessionId": sess, "timestamp": "2026-09-01T10:00:01Z",
		"message": map[string]any{"content": []map[string]any{{"type": "tool_result", "tool_use_id": toolID, "content": content}}},
	}
}

func writeJSONL(t *testing.T, path string, lines ...map[string]any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	for _, l := range lines {
		b, err := json.Marshal(l)
		if err != nil {
			t.Fatal(err)
		}
		sb.Write(b)
		sb.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// sonnet-5 list price: in 2, out 10, cache write 2.5, cache read 0.2 ($/MTok)
const sonnet = "claude-sonnet-5"

func mustParse(t *testing.T, path, proj string) *session {
	t.Helper()
	s := parseSession(path, proj)
	if s == nil {
		t.Fatalf("parseSession(%s) = nil", path)
	}
	return s
}

// ---- parsing ----------------------------------------------------------------

func TestParseDedupesMessageIDKeepingLastUsage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s1.jsonl")
	writeJSONL(t, path,
		// Claude Code writes one line per content block: same id, usage grows.
		assistant("s1", "m1", sonnet, usg{"input_tokens": 10, "output_tokens": 5, "cache_creation_input_tokens": 100, "cache_read_input_tokens": 1000}),
		assistant("s1", "m1", sonnet, usg{"input_tokens": 10, "output_tokens": 50, "cache_creation_input_tokens": 100, "cache_read_input_tokens": 1000}),
		assistant("s1", "m2", sonnet, usg{"input_tokens": 1, "output_tokens": 1}),
	)
	s := mustParse(t, path, "p")
	if len(s.turns) != 2 {
		t.Fatalf("turns = %d, want 2 (duplicate ids must collapse)", len(s.turns))
	}
	if got := s.turns[0].usage.Out; got != 50 {
		t.Errorf("first turn output = %d, want final value 50", got)
	}
}

func TestPriceAndCost(t *testing.T) {
	p, ok := priceFor("claude-sonnet-5")
	if !ok {
		t.Fatal("sonnet-5 should be priced")
	}
	u := usageBlock{In: 10, Out: 50, CacheCreate: 100, CacheRead: 1000}
	want := (10*2.0 + 50*10.0 + 100*2.5 + 1000*0.2) / 1e6
	if got := p.cost(u); !near(got, want) {
		t.Errorf("cost = %v, want %v", got, want)
	}
	if _, ok := priceFor("claude-haiku-4-5-20251001"); !ok {
		t.Error("date-suffixed model ids should resolve to the base price")
	}
	if _, ok := priceFor("claude-future-9"); ok {
		t.Error("unknown model must not be priced")
	}
}

func TestCarriedCost(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s1.jsonl")
	writeJSONL(t, path,
		assistant("s1", "m1", sonnet, usg{"output_tokens": 1}, toolUse("t1", "Bash", nil)),
		toolResult("s1", "t1", strings.Repeat("x", 400)), // 400 chars -> 100 tokens
		assistant("s1", "m2", sonnet, usg{"output_tokens": 1}),
		assistant("s1", "m3", sonnet, usg{"output_tokens": 1}),
		assistant("s1", "m4", sonnet, usg{"output_tokens": 1}),
	)
	s := mustParse(t, path, "p")
	if len(s.results) != 1 || s.results[0].tokens != 100 || s.results[0].at != 1 {
		t.Fatalf("results = %+v", s.results)
	}
	// seen first by turn index 1 (cache write), then re-read by turns 2 and 3.
	want := 100 * (2.5 + 0.2*2) / 1e6
	if got := s.carried(s.results[0]); !near(got, want) {
		t.Errorf("carried = %v, want %v", got, want)
	}
}

// ---- aggregation ------------------------------------------------------------

func TestSubagentCostRollsIntoParentButNotSessionStats(t *testing.T) {
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "proj", "sess.jsonl")
	subPath := filepath.Join(dir, "proj", "sess", "subagents", "agent-1.jsonl")
	writeJSONL(t, mainPath, assistant("sess", "m1", sonnet, usg{"output_tokens": 1000, "cache_read_input_tokens": 500_000}))
	writeJSONL(t, subPath, assistant("sess", "a1", sonnet, usg{"output_tokens": 2000, "cache_read_input_tokens": 50_000}))

	main, sub := mustParse(t, mainPath, "proj"), mustParse(t, subPath, "proj")
	if !sub.sub || sub.parent != "sess" || main.sub {
		t.Fatalf("sub=%v parent=%q main.sub=%v", sub.sub, sub.parent, main.sub)
	}
	r := analyze([]*session{main, sub})

	if r.Sessions != 1 || r.SubagentRuns != 1 {
		t.Errorf("sessions=%d subagentRuns=%d, want 1 and 1", r.Sessions, r.SubagentRuns)
	}
	if r.LongSessions != 1 {
		t.Errorf("only the main session (500K ctx) is long; got %d", r.LongSessions)
	}
	p, _ := priceFor(sonnet)
	mainCost := p.cost(usageBlock{Out: 1000, CacheRead: 500_000})
	subCost := p.cost(usageBlock{Out: 2000, CacheRead: 50_000})
	if !near(r.Cost, mainCost+subCost) || !near(r.SubagentUSD, subCost) {
		t.Errorf("cost=%v sub=%v, want %v and %v", r.Cost, r.SubagentUSD, mainCost+subCost, subCost)
	}
	top := r.TopSessions[0]
	if !near(top.Cost, mainCost+subCost) || !near(top.SubUSD, subCost) {
		t.Errorf("top session cost=%v subUSD=%v", top.Cost, top.SubUSD)
	}
	if top.PeakCtx != 500_000 {
		t.Errorf("peak ctx = %d; subagent context must not leak into the parent", top.PeakCtx)
	}
}

func TestUnpricedModelIsReportedNotSilentlyCounted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	writeJSONL(t, path,
		assistant("s", "m1", "claude-future-9", usg{"output_tokens": 1_000_000}),
		assistant("s", "m2", "<synthetic>", usg{"output_tokens": 1_000_000}),
	)
	r := analyze([]*session{mustParse(t, path, "p")})
	if r.UnpricedTurns != 1 || r.Cost != 0 {
		t.Errorf("unpriced=%d cost=%v, want 1 and 0", r.UnpricedTurns, r.Cost)
	}
	if r.Turns != 1 {
		t.Errorf("synthetic turns must be ignored; turns=%d", r.Turns)
	}
}

func TestRepeatReadsCountOnlyRepeatsOfFilesReadThreeTimes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	lines := []map[string]any{}
	for i, id := range []string{"r1", "r2", "r3", "r4"} {
		file := "/a.go"
		if i == 3 {
			file = "/b.go" // read once: never counts
		}
		lines = append(lines,
			assistant("s", "m"+id, sonnet, usg{"output_tokens": 1}, toolUse(id, "Read", map[string]any{"file_path": file})),
			toolResult("s", id, strings.Repeat("x", 400)))
	}
	lines = append(lines, assistant("s", "mend", sonnet, usg{"output_tokens": 1}))
	writeJSONL(t, path, lines...)
	r := analyze([]*session{mustParse(t, path, "p")})
	// /a.go read 3x: first is useful, two repeats of 100 tokens are waste.
	if r.RereadTokens != 200 {
		t.Errorf("RereadTokens = %d, want 200", r.RereadTokens)
	}
	if len(r.RepeatReads) != 1 || r.RepeatReads[0].Count != 3 {
		t.Errorf("RepeatReads = %+v", r.RepeatReads)
	}
}

// ---- opportunities ----------------------------------------------------------

func TestOpportunitiesRankedByDollarsAndTinyOnesDropped(t *testing.T) {
	r := report{
		Sessions: 5, Turns: 100, Cost: 1000, CacheHitPct: 95,
		CapSaveUSD:   400,
		ToolCarryUSD: map[string]float64{"Bash": 400, "Read": 10},
		ToolTokens:   map[string]int{"Bash": 1000, "Read": 100},
		Models:       map[string]int{sonnet: 100},
		RereadUSD:    4, RepeatReads: []repeatRead{{File: "/a.go", Count: 4}},
	}
	got := opportunities(r)
	var titles []string
	for _, o := range got {
		titles = append(titles, o.Title)
	}
	if len(got) < 2 || got[0].Title != "Keep sessions under ~200K context" || got[1].Title != "Bash output is heavy" {
		t.Fatalf("order = %v", titles)
	}
	for _, o := range got {
		if strings.Contains(o.Title, "Read output") || strings.Contains(o.Title, "re-reading") {
			t.Errorf("%q saves <1%% of spend and should be dropped", o.Title)
		}
		if o.SaveLowUSD > o.SaveUSD {
			t.Errorf("%q: low %v > high %v", o.Title, o.SaveLowUSD, o.SaveUSD)
		}
	}
	if got[0].Impact != "high" {
		t.Errorf("a 20-40%% saving should be high impact, got %q", got[0].Impact)
	}
}

func TestOpportunitiesEmptyOnNoData(t *testing.T) {
	if got := opportunities(report{}); len(got) != 0 {
		t.Errorf("got %v", got)
	}
}

// ---- helpers & rendering ------------------------------------------------------

func TestSmallHelpers(t *testing.T) {
	if got := projectDirName("/Users/a/Proj/com.x.y-z"); got != "-Users-a-Proj-com-x-y-z" {
		t.Errorf("projectDirName = %q", got)
	}
	for in, want := range map[string]string{
		"claude-sonnet-5-5": "Sonnet 5.5", "claude-opus-5": "Opus 5", "claude-haiku-4-5-20251001": "Haiku 4.5",
	} {
		if got := prettyModel(in); got != want {
			t.Errorf("prettyModel(%q) = %q, want %q", in, got, want)
		}
	}
	if got := toolLabel("mcp__claude_ai_Atlassian__search"); got != "Atlassian › search" {
		t.Errorf("toolLabel = %q", got)
	}
	if got := compact(2_747_131_371); got != "2.7B" {
		t.Errorf("compact = %q", got)
	}
	for in, want := range map[float64]string{5: "$5.00", 0.043: "$0.043", 1234.4: "$1,234"} {
		if got := money(in); got != want {
			t.Errorf("money(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestRenderDoesNotPanicOnEmptyOrFullReport(t *testing.T) {
	useColor = false
	renderReport(analyze(nil), true)

	path := filepath.Join(t.TempDir(), "s.jsonl")
	writeJSONL(t, path,
		assistant("s", "m1", sonnet, usg{"output_tokens": 10, "cache_read_input_tokens": 600_000}, toolUse("t", "Bash", nil)),
		toolResult("s", "t", strings.Repeat("x", 4000)),
		assistant("s", "m2", sonnet, usg{"output_tokens": 10, "cache_read_input_tokens": 600_000}),
	)
	renderReport(analyze([]*session{mustParse(t, path, "-Users-x-proj")}), true)
	renderReport(analyze([]*session{mustParse(t, path, "-Users-x-proj")}), false)
}

func TestOneHourCacheWritesCostTwiceInput(t *testing.T) {
	p, _ := priceFor("claude-opus-5") // official: input 5, 5m write 6.25, 1h write 10, read 0.50
	u := usageBlock{CacheCreate: 1_000_000}
	u.CacheCreation.Eph5m, u.CacheCreation.Eph1h = 400_000, 600_000
	if got, want := p.cost(u), 0.4*6.25+0.6*10.0; !near(got, want) {
		t.Errorf("split write cost = %v, want %v", got, want)
	}
	// No breakdown (old transcripts): everything is priced as a 5-minute write.
	if got, want := p.cost(usageBlock{CacheCreate: 1_000_000}), 6.25; !near(got, want) {
		t.Errorf("unsplit write cost = %v, want %v", got, want)
	}
	// A breakdown larger than the total can't create tokens.
	bad := usageBlock{CacheCreate: 100}
	bad.CacheCreation.Eph1h = 500
	if m5, h1 := bad.writeTokens(); m5 != 0 || h1 != 100 {
		t.Errorf("writeTokens = %d,%d, want 0,100", m5, h1)
	}
}

func TestOfficialPriceSpotChecks(t *testing.T) {
	// Values from the published pricing table; Fable 5.1 reads at 0.025x, Opus 5.5 at 0.05x.
	for model, want := range map[string]price{
		"claude-fable-5-1":  {10, 50, 12.5, 20, 0.25},
		"claude-fable-5":    {10, 50, 12.5, 20, 1},
		"claude-opus-5-5":   {4, 20, 5, 8, 0.20},
		"claude-sonnet-5-5": {2, 10, 2.5, 4, 0.20},
	} {
		if got, _ := priceFor(model); got != want {
			t.Errorf("%s = %+v, want %+v", model, got, want)
		}
	}
}
