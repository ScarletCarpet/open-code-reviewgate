package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/open-code-review/open-code-review/internal/agent"
	"github.com/open-code-review/open-code-review/internal/model"
)

type mockResultProvider struct {
	diffs            []model.Diff
	filesReviewed    int64
	inputTokens      int64
	outputTokens     int64
	totalTokens      int64
	cacheReadTokens  int64
	cacheWriteTokens int64
	warnings         []agent.AgentWarning
	projectSummary   string
	toolCalls        map[string]int64
}

func (m *mockResultProvider) Diffs() []model.Diff            { return m.diffs }
func (m *mockResultProvider) FilesReviewed() int64           { return m.filesReviewed }
func (m *mockResultProvider) TotalInputTokens() int64        { return m.inputTokens }
func (m *mockResultProvider) TotalOutputTokens() int64       { return m.outputTokens }
func (m *mockResultProvider) TotalTokensUsed() int64         { return m.totalTokens }
func (m *mockResultProvider) TotalCacheReadTokens() int64    { return m.cacheReadTokens }
func (m *mockResultProvider) TotalCacheWriteTokens() int64   { return m.cacheWriteTokens }
func (m *mockResultProvider) Warnings() []agent.AgentWarning { return m.warnings }
func (m *mockResultProvider) ProjectSummary() string         { return m.projectSummary }
func (m *mockResultProvider) ToolCalls() map[string]int64    { return m.toolCalls }

func TestEmitRunResult_JSONNoFiles(t *testing.T) {
	ag := &mockResultProvider{filesReviewed: 0}
	got := captureStdout(t, func() {
		err := emitRunResult(context.Background(), ag, nil, time.Now(), "json", "developer", nil, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	var out jsonOutput
	if err := json.Unmarshal([]byte(got), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Status != "skipped" {
		t.Errorf("status = %q, want skipped", out.Status)
	}
}

func TestEmitRunResult_JSONWithComments(t *testing.T) {
	ag := &mockResultProvider{
		filesReviewed: 3,
		inputTokens:   100,
		outputTokens:  50,
		totalTokens:   150,
		warnings:      []agent.AgentWarning{{Type: "info", Message: "note"}},
		toolCalls:     map[string]int64{"file_read": 2},
	}
	comments := []model.LlmComment{{Path: "main.go", Content: "fix", StartLine: 1, EndLine: 2}}
	got := captureStdout(t, func() {
		err := emitRunResult(context.Background(), ag, comments, time.Now(), "json", "developer", nil, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	var out jsonOutput
	if err := json.Unmarshal([]byte(got), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Comments) != 1 {
		t.Errorf("expected 1 comment, got %d", len(out.Comments))
	}
	if out.Summary == nil || out.Summary.FilesReviewed != 3 {
		t.Errorf("summary.FilesReviewed = %v", out.Summary)
	}
}

func TestEmitRunResult_TextNoComments(t *testing.T) {
	ag := &mockResultProvider{filesReviewed: 2}
	got := captureStdout(t, func() {
		err := emitRunResult(context.Background(), ag, nil, time.Now(), "text", "developer", nil, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	if !strings.Contains(got, "Looks good to me") {
		t.Errorf("expected 'Looks good to me', got %q", got)
	}
}

func TestEmitRunResult_TextWithComments(t *testing.T) {
	ag := &mockResultProvider{filesReviewed: 1}
	comments := []model.LlmComment{{Path: "a.go", Content: "rename", StartLine: 5, EndLine: 10}}
	got := captureStdout(t, func() {
		err := emitRunResult(context.Background(), ag, comments, time.Now(), "text", "developer", nil, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	if !strings.Contains(got, "a.go") {
		t.Errorf("expected path, got %q", got)
	}
	if !strings.Contains(got, "rename") {
		t.Errorf("expected comment content, got %q", got)
	}
}

func TestEmitRunResult_TextWithProjectSummary(t *testing.T) {
	ag := &mockResultProvider{
		filesReviewed:  5,
		projectSummary: "All tests pass, code quality is good.",
	}
	got := captureStdout(t, func() {
		err := emitRunResult(context.Background(), ag, nil, time.Now(), "text", "developer", nil, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	if !strings.Contains(got, "Project Summary") {
		t.Errorf("expected 'Project Summary', got %q", got)
	}
	if !strings.Contains(got, "All tests pass") {
		t.Errorf("expected summary content, got %q", got)
	}
}

func TestEmitRunResult_AgentTextRestoresQuiet(t *testing.T) {
	ag := &mockResultProvider{filesReviewed: 1}
	q := newQuietHandle("text", "agent")
	got := captureStdout(t, func() {
		err := emitRunResult(context.Background(), ag, nil, time.Now(), "text", "agent", q, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	if q.fn != nil {
		t.Error("expected quiet handle to be restored")
	}
	_ = got
}

func TestEmitRunResult_AgentJSONDoesNotRestore(t *testing.T) {
	ag := &mockResultProvider{
		filesReviewed: 1,
		inputTokens:   10,
		outputTokens:  5,
		totalTokens:   15,
	}
	q := newQuietHandle("json", "agent")
	got := captureStdout(t, func() {
		err := emitRunResult(context.Background(), ag, nil, time.Now(), "json", "agent", q, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	var out jsonOutput
	if err := json.Unmarshal([]byte(got), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	q.Restore()
}

func TestEmitRunResult_NilQuietHandle(t *testing.T) {
	ag := &mockResultProvider{filesReviewed: 1}
	got := captureStdout(t, func() {
		err := emitRunResult(context.Background(), ag, nil, time.Now(), "text", "agent", nil, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	_ = got
}

func TestEmitRunResult_FilteredNoMatchShowsMessage(t *testing.T) {
	fc := parseFilterFlags("low", "")
	ag := &mockResultProvider{filesReviewed: 1}
	comments := []model.LlmComment{
		{Path: "a.go", Severity: "high", Category: "bug", Content: "should be filtered out"},
	}
	got := captureStdout(t, func() {
		err := emitRunResult(context.Background(), ag, comments, time.Now(), "text", "developer", nil, fc)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	if !strings.Contains(got, "No comments generated. Looks good to me.\n") {
		t.Errorf("expected 'No comments match', got %q", got)
	}
}

func TestEmitRunResult_SortedBySeverityAndCategory(t *testing.T) {
	fc := parseFilterFlags("", "")
	ag := &mockResultProvider{filesReviewed: 1}
	comments := []model.LlmComment{
		{Path: "c.go", Severity: "low", Category: "bug", Content: "low bug", StartLine: 1, EndLine: 2},
		{Path: "a.go", Severity: "high", Category: "security", Content: "high sec", StartLine: 1, EndLine: 2},
		{Path: "b.go", Severity: "medium", Category: "bug", Content: "mid bug", StartLine: 1, EndLine: 2},
	}
	got := captureStdout(t, func() {
		err := emitRunResult(context.Background(), ag, comments, time.Now(), "text", "developer", nil, fc)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	// a.go (high·security) should appear first, then b.go (medium·bug), then c.go (low·bug)
	aIdx := strings.Index(got, "a.go")
	bIdx := strings.Index(got, "b.go")
	cIdx := strings.Index(got, "c.go")
	if aIdx < 0 || bIdx < 0 || cIdx < 0 {
		t.Fatal("expected all three files in output")
	}
	if !(aIdx < bIdx && bIdx < cIdx) {
		t.Errorf("expected order a.go -> b.go -> c.go, got a=%d b=%d c=%d", aIdx, bIdx, cIdx)
	}
}

func TestEmitRunResult_FilteredWithCountHint(t *testing.T) {
	fc := parseFilterFlags("high", "")
	ag := &mockResultProvider{filesReviewed: 1}
	comments := []model.LlmComment{
		{Path: "a.go", Severity: "high", Category: "bug", Content: "keep"},
		{Path: "b.go", Severity: "medium", Category: "style", Content: "hidden"},
		{Path: "c.go", Severity: "low", Category: "docs", Content: "hidden"},
	}
	got := captureStdout(t, func() {
		err := emitRunResult(context.Background(), ag, comments, time.Now(), "text", "developer", nil, fc)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	if !strings.Contains(got, "a.go") {
		t.Errorf("expected a.go in output, got %q", got)
	}
	if strings.Contains(got, "b.go") || strings.Contains(got, "c.go") {
		t.Errorf("did not expect filtered-out files in output, got %q", got)
	}
}

func TestEmitRunResult_JSONFilteredByLevel(t *testing.T) {
	fc := parseFilterFlags("high", "")
	ag := &mockResultProvider{
		filesReviewed: 2,
		inputTokens:   100,
		outputTokens:  50,
		totalTokens:   150,
		toolCalls:     map[string]int64{"file_read": 1},
	}
	comments := []model.LlmComment{
		{Path: "a.go", Severity: "high", Category: "bug", Content: "keep"},
		{Path: "b.go", Severity: "medium", Category: "style", Content: "hidden"},
	}
	got := captureStdout(t, func() {
		err := emitRunResult(context.Background(), ag, comments, time.Now(), "json", "developer", nil, fc)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	var out jsonOutput
	if err := json.Unmarshal([]byte(got), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Comments) != 1 {
		t.Errorf("expected 1 comment after filter, got %d", len(out.Comments))
	}
	if out.Comments[0].Path != "a.go" {
		t.Errorf("expected a.go, got %s", out.Comments[0].Path)
	}
}
