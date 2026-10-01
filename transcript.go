package main

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// ---- transcript model -------------------------------------------------------
// Claude Code writes one JSONL line per content block, so an assistant message
// (same message.id) appears several times. We dedupe by id and keep the last
// usage block, which carries the final output_tokens.

type usageBlock struct {
	In          int `json:"input_tokens"`
	Out         int `json:"output_tokens"`
	CacheRead   int `json:"cache_read_input_tokens"`
	CacheCreate int `json:"cache_creation_input_tokens"`
}

type contentBlock struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
}

type tLine struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	Timestamp string `json:"timestamp"`
	Sidechain bool   `json:"isSidechain"`
	Message   struct {
		ID      string          `json:"id"`
		Model   string          `json:"model"`
		Usage   *usageBlock     `json:"usage"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// resultRec is one tool result. at is the index of the first assistant turn that
// could see it; every later turn re-reads it from the cache.
type resultRec struct {
	tool, file string
	tokens, at int
}

type turn struct {
	model     string
	usage     usageBlock
	sidechain bool
}

type session struct {
	id        string
	proj      string
	turns     []turn
	byID      map[string]int // message.id -> index in turns
	toolName  map[string]string
	toolFile  map[string]string // tool_use id -> file_path for Read
	readChars map[string]int    // file_path -> total chars returned across all reads
	sub       bool              // a subagent run (<session>/subagents/agent-*.jsonl)
	parent    string            // parent session id, for subagent runs
	results   []resultRec
	reads     map[string]int
	toolChars map[string]int
	start     time.Time
}

// ctx is the prompt size the model saw on that turn.
func (u usageBlock) ctx() int { return u.In + u.CacheRead + u.CacheCreate }

func parseSession(path, proj string) *session {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	s := &session{
		id: strings.TrimSuffix(filepath.Base(path), ".jsonl"), proj: proj,
		byID: map[string]int{}, toolName: map[string]string{}, toolFile: map[string]string{},
		reads: map[string]int{}, toolChars: map[string]int{}, readChars: map[string]int{},
	}
	s.sub = strings.Contains(path, string(filepath.Separator)+"subagents"+string(filepath.Separator))
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		var l tLine
		if json.Unmarshal(sc.Bytes(), &l) != nil {
			continue
		}
		if s.sub && s.parent == "" {
			s.parent = l.SessionID
		}
		if s.start.IsZero() {
			s.start, _ = time.Parse(time.RFC3339, l.Timestamp)
		}
		var blocks []contentBlock
		_ = json.Unmarshal(l.Message.Content, &blocks) // string content (plain prompts) is ignored

		switch l.Type {
		case "assistant":
			if l.Message.Usage != nil {
				t := turn{model: l.Message.Model, usage: *l.Message.Usage, sidechain: l.Sidechain}
				if i, ok := s.byID[l.Message.ID]; ok && l.Message.ID != "" {
					s.turns[i] = t
				} else {
					s.byID[l.Message.ID] = len(s.turns)
					s.turns = append(s.turns, t)
				}
			}
			for _, b := range blocks {
				if b.Type != "tool_use" {
					continue
				}
				if _, seen := s.toolName[b.ID]; seen {
					continue
				}
				s.toolName[b.ID] = b.Name
				if b.Name == "Read" {
					var in struct {
						FilePath string `json:"file_path"`
					}
					if json.Unmarshal(b.Input, &in) == nil && in.FilePath != "" {
						s.toolFile[b.ID] = in.FilePath
						s.reads[in.FilePath]++
					}
				}
			}
		case "user":
			for _, b := range blocks {
				if b.Type == "tool_result" {
					if name := s.toolName[b.ToolUseID]; name != "" {
						n := resultLen(b.Content)
						s.toolChars[name] += n
						s.results = append(s.results, resultRec{tool: name, file: s.toolFile[b.ToolUseID], tokens: n / 4, at: len(s.turns)})
						if f := s.toolFile[b.ToolUseID]; f != "" {
							s.readChars[f] += n
						}
					}
				}
			}
		}
	}
	return s
}

func resultLen(raw json.RawMessage) int {
	var str string
	if json.Unmarshal(raw, &str) == nil {
		return len(str)
	}
	var parts []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) == nil {
		n := 0
		for _, p := range parts {
			n += len(p.Text)
		}
		return n
	}
	return 0
}

// ---- discovery --------------------------------------------------------------

var nonAlnum = regexp.MustCompile(`[^A-Za-z0-9]`)

// projectDirName mirrors how Claude Code names ~/.claude/projects/<dir>.
func projectDirName(cwd string) string { return nonAlnum.ReplaceAllString(cwd, "-") }

func transcriptFiles(global bool, since time.Time) (files, projs []string) {
	root := filepath.Join(homeDir(), ".claude", "projects")
	proj := "*"
	if !global {
		cwd, _ := os.Getwd()
		proj = projectDirName(cwd)
	}
	main, _ := filepath.Glob(filepath.Join(root, proj, "*.jsonl"))
	subs, _ := filepath.Glob(filepath.Join(root, proj, "*", "subagents", "agent-*.jsonl"))
	for _, m := range append(main, subs...) {
		if !since.IsZero() {
			if fi, err := os.Stat(m); err != nil || fi.ModTime().Before(since) {
				continue
			}
		}
		rel, _ := filepath.Rel(root, m)
		files = append(files, m)
		projs = append(projs, strings.SplitN(rel, string(filepath.Separator), 2)[0])
	}
	return
}
