package filters

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func openSearchResponse(n int) string {
	hits := make([]map[string]interface{}, n)
	for i := 0; i < n; i++ {
		status := "DONE"
		if i%3 == 0 {
			status = "OPEN"
		}
		hits[i] = map[string]interface{}{
			"_index": "vehicle-tasks-v3",
			"_id":    fmt.Sprintf("task-%d", 10000+i),
			"_score": 1.0 - float64(i)*0.001,
			"_source": map[string]interface{}{
				"taskId":     fmt.Sprintf("task-%d", 10000+i),
				"vehicleId":  fmt.Sprintf("WDB%d", 2000000+i),
				"branchId":   2853,
				"type":       "CLEANLINESS",
				"status":     status,
				"assignee":   nil,
				"notes":      "",
				"directives": []interface{}{},
				"raw":        strings.Repeat("x", 400),
			},
		}
	}
	b, _ := json.Marshal(map[string]interface{}{
		"took": 14,
		"hits": map[string]interface{}{
			"total": map[string]interface{}{"value": 1284, "relation": "eq"},
			"hits":  hits,
		},
	})
	return string(b)
}

func grpcDump(n int) string {
	tasks := make([]map[string]interface{}, n)
	for i := 0; i < n; i++ {
		tasks[i] = map[string]interface{}{
			"id":      fmt.Sprintf("task-%d", i),
			"vehicle": map[string]interface{}{"id": fmt.Sprintf("v-%d", i), "model": "", "color": ""},
			"meta":    map[string]interface{}{"source": "", "retries": 0, "tags": []interface{}{}},
			"payload": strings.Repeat("p", 300),
		}
	}
	b, _ := json.Marshal(map[string]interface{}{
		"tasks": tasks, "nextPageToken": "", "errors": []interface{}{},
	})
	return string(b)
}

func grepDump() string {
	files := []string{"src/A.java", "src/B.java", "src/C.java"}
	var lines []string
	for _, f := range files {
		for i := 1; i <= 15; i++ {
			lines = append(lines, fmt.Sprintf("%s:%d:    log.info(M, \"task {}\", id);", f, i*7))
		}
	}
	return strings.Join(lines, "\n")
}

func verboseLog(n int) string {
	var lines []string
	for i := 0; i < n; i++ {
		lines = append(lines, "DEBUG pool acquiring connection")
		lines = append(lines, "DEBUG pool acquiring connection")
		lines = append(lines, fmt.Sprintf("INFO  request %d handled", i))
	}
	return strings.Join(lines, "\n")
}

func TestOpenSearch(t *testing.T) {
	r, ok := Compress("mcp__prod-opensearch__SearchIndexTool", openSearchResponse(50), DefaultConfig())
	if !ok || r.Kind != "opensearch" {
		t.Fatalf("expected opensearch compression, got %+v ok=%v", r, ok)
	}
	if r.Gain < 0.7 {
		t.Errorf("expected >70%% gain, got %.2f", r.Gain)
	}
	var p map[string]interface{}
	if err := json.Unmarshal([]byte(r.Text), &p); err != nil {
		t.Fatalf("output not valid JSON: %v", err)
	}
	if p["total"].(float64) != 1284 {
		t.Errorf("total not preserved: %v", p["total"])
	}
	if len(p["hits"].([]interface{})) != 5 {
		t.Errorf("hits not capped to 5: %v", p["hits"])
	}
	if p["omitted"].(float64) != 45 {
		t.Errorf("omitted wrong: %v", p["omitted"])
	}
}

func TestGenericJSON(t *testing.T) {
	r, ok := Compress("mcp__production-debugger__query_service", grpcDump(40), DefaultConfig())
	if !ok {
		t.Fatal("expected compression")
	}
	if !json.Valid([]byte(r.Text)) {
		t.Error("output not valid JSON")
	}
	if !strings.Contains(r.Text, "of 40") {
		t.Error("array not capped")
	}
	if strings.Contains(r.Text, "nextPageToken") {
		t.Error("empty field not dropped")
	}
}

func TestGrep(t *testing.T) {
	r, ok := Compress("Grep", grepDump(), DefaultConfig())
	if !ok || r.Kind != "grep" {
		t.Fatalf("expected grep, got %+v ok=%v", r, ok)
	}
	if !strings.Contains(r.Text, "more in") {
		t.Error("per-file cap missing")
	}
}

func TestLogDedupe(t *testing.T) {
	r, ok := Compress("Bash", verboseLog(300), DefaultConfig())
	if !ok {
		t.Fatal("expected compression")
	}
	if !strings.Contains(r.Text, "⟨×2⟩") {
		t.Error("run-length collapse missing")
	}
}

func TestSafetyNoOps(t *testing.T) {
	if _, ok := Compress("Bash", "all good", DefaultConfig()); ok {
		t.Error("small output should be left alone")
	}
	if _, ok := Compress("Bash", `{"ok":true}`, DefaultConfig()); ok {
		t.Error("tiny json should be left alone")
	}
	broken := "{ this is not json " + strings.Repeat("x", 2000)
	if r, ok := Compress("Bash", broken, DefaultConfig()); ok && r.Kind != "text" {
		t.Errorf("broken json should fall back to text, got %s", r.Kind)
	}
}

func TestSourceProjection(t *testing.T) {
	cfg := DefaultConfig()
	cfg.SourceFields = []string{"taskId", "status"}
	r, _ := Compress("mcp__os__Search", openSearchResponse(50), cfg)
	if strings.Contains(r.Text, "vehicleId") {
		t.Error("projection should drop vehicleId")
	}
	if !strings.Contains(r.Text, "taskId") {
		t.Error("projection should keep taskId")
	}
}
