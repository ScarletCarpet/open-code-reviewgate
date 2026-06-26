package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/open-code-review/open-code-review/internal/config/toolsconfig"
	"github.com/open-code-review/open-code-review/internal/llm"
	"github.com/open-code-review/open-code-review/internal/model"
)

func TestBuildFilterCommentsJSON(t *testing.T) {
	tests := []struct {
		name     string
		comments []model.LlmComment
		wantIDs  []string
	}{
		{
			name:     "empty slice",
			comments: nil,
			wantIDs:  nil,
		},
		{
			name: "single comment",
			comments: []model.LlmComment{
				{Content: "fix this", ExistingCode: "old code"},
			},
			wantIDs: []string{"c-0"},
		},
		{
			name: "multiple comments sequential IDs",
			comments: []model.LlmComment{
				{Content: "issue A"},
				{Content: "issue B", ExistingCode: "existing"},
				{Content: "issue C"},
			},
			wantIDs: []string{"c-0", "c-1", "c-2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildFilterCommentsJSON(tt.comments)

			var items []struct {
				ID           string `json:"id"`
				Content      string `json:"content"`
				ExistingCode string `json:"existing_code,omitempty"`
			}
			if err := json.Unmarshal([]byte(got), &items); err != nil {
				t.Fatalf("invalid JSON: %v\nraw: %s", err, got)
			}

			if len(items) != len(tt.comments) {
				t.Fatalf("len = %d, want %d", len(items), len(tt.comments))
			}

			for i, item := range items {
				if tt.wantIDs != nil && item.ID != tt.wantIDs[i] {
					t.Errorf("items[%d].ID = %q, want %q", i, item.ID, tt.wantIDs[i])
				}
				if item.Content != tt.comments[i].Content {
					t.Errorf("items[%d].Content = %q, want %q", i, item.Content, tt.comments[i].Content)
				}
				if item.ExistingCode != tt.comments[i].ExistingCode {
					t.Errorf("items[%d].ExistingCode = %q, want %q", i, item.ExistingCode, tt.comments[i].ExistingCode)
				}
			}
		})
	}
}

func TestParseFilterResponse(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		total   int
		wantSet map[int]struct{}
	}{
		{
			name:    "valid JSON array",
			raw:     `["c-0","c-2","c-4"]`,
			total:   5,
			wantSet: map[int]struct{}{0: {}, 2: {}, 4: {}},
		},
		{
			name:    "markdown fenced JSON",
			raw:     "```json\n[\"c-1\"]\n```",
			total:   3,
			wantSet: map[int]struct{}{1: {}},
		},
		{
			name:    "out-of-range indices ignored",
			raw:     `["c-0","c-10","c-99"]`,
			total:   5,
			wantSet: map[int]struct{}{0: {}},
		},
		{
			name:    "negative index ignored",
			raw:     `["c--1","c-0"]`,
			total:   2,
			wantSet: map[int]struct{}{0: {}},
		},
		{
			name:    "invalid ID format ignored",
			raw:     `["x-0","c-abc","c-1"]`,
			total:   3,
			wantSet: map[int]struct{}{1: {}},
		},
		{
			name:    "invalid JSON returns nil",
			raw:     `not json`,
			total:   5,
			wantSet: nil,
		},
		{
			name:    "empty array",
			raw:     `[]`,
			total:   5,
			wantSet: map[int]struct{}{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseFilterResponse(tt.raw, tt.total)
			if tt.wantSet == nil {
				if got != nil {
					t.Errorf("expected nil, got %v", got)
				}
				return
			}
			if len(got) != len(tt.wantSet) {
				t.Fatalf("len = %d, want %d; got %v", len(got), len(tt.wantSet), got)
			}
			for idx := range tt.wantSet {
				if _, ok := got[idx]; !ok {
					t.Errorf("missing index %d in result", idx)
				}
			}
		})
	}
}

func TestExtFromPath(t *testing.T) {
	a := New(Args{})

	tests := []struct {
		path string
		want string
	}{
		{"main.go", ".go"},
		{"src/app.tsx", ".tsx"},
		{"path/to/FILE.JSON", ".json"},
		{"Makefile", ""},
		{".gitignore", ""},
		{"dir/.hidden", ""},
		{"archive.tar.gz", ".gz"},
		{"no-ext", ""},
		{"path/to/", ""},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := a.extFromPath(tt.path)
			if got != tt.want {
				t.Errorf("extFromPath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestFormatToolDefs(t *testing.T) {
	t.Run("empty defs returns empty string", func(t *testing.T) {
		got := formatToolDefs(nil)
		if got != "" {
			t.Errorf("expected empty, got %q", got)
		}
	})

	t.Run("single tool with parameters", func(t *testing.T) {
		defs := []llm.ToolDef{
			{
				Type: "function",
				Function: llm.FunctionDef{
					Name:        "file_read",
					Description: "Read a file from the repository",
					Parameters: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"path": map[string]any{
								"type":        "string",
								"description": "File path to read",
							},
							"start_line": map[string]any{
								"type":        "integer",
								"description": "Starting line number",
							},
						},
						"required": []any{"path"},
					},
				},
			},
		}
		got := formatToolDefs(defs)
		if !strings.Contains(got, "### Available Tools") {
			t.Error("missing header")
		}
		if !strings.Contains(got, "**file_read**") {
			t.Error("missing tool name")
		}
		if !strings.Contains(got, "Read a file from the repository") {
			t.Error("missing description")
		}
		if !strings.Contains(got, "path") {
			t.Error("missing parameter name")
		}
		if !strings.Contains(got, "(required)") {
			t.Error("missing required marker")
		}
	})

	t.Run("tool without parameters", func(t *testing.T) {
		defs := []llm.ToolDef{
			{
				Type: "function",
				Function: llm.FunctionDef{
					Name:        "task_done",
					Description: "Signal task completion",
					Parameters:  map[string]any{},
				},
			},
		}
		got := formatToolDefs(defs)
		if !strings.Contains(got, "**task_done**") {
			t.Error("missing tool name")
		}
		if strings.Contains(got, "Parameters:") {
			t.Error("should not show Parameters section for empty params")
		}
	})

	t.Run("multiple tools", func(t *testing.T) {
		defs := []llm.ToolDef{
			{Type: "function", Function: llm.FunctionDef{Name: "tool_a", Description: "desc a"}},
			{Type: "function", Function: llm.FunctionDef{Name: "tool_b", Description: "desc b"}},
		}
		got := formatToolDefs(defs)
		if !strings.Contains(got, "tool_a") || !strings.Contains(got, "tool_b") {
			t.Errorf("missing tools in output: %s", got)
		}
	})
}

func TestBuildToolDefs(t *testing.T) {
	funcDef := json.RawMessage(`{"name":"test_tool","description":"a tool","parameters":{}}`)

	entries := []toolsconfig.ToolConfigEntry{
		{Name: "plan_only", PlanTask: true, MainTask: false, Definition: funcDef},
		{Name: "main_only", PlanTask: false, MainTask: true, Definition: funcDef},
		{Name: "both", PlanTask: true, MainTask: true, Definition: funcDef},
		{Name: "neither", PlanTask: false, MainTask: false, Definition: funcDef},
	}

	t.Run("planOnly=true returns plan_task tools", func(t *testing.T) {
		defs := BuildToolDefs(entries, true)
		if len(defs) != 2 {
			t.Fatalf("expected 2 defs, got %d", len(defs))
		}
		names := make(map[string]bool)
		for _, d := range defs {
			names[d.Function.Name] = true
		}
		if !names["test_tool"] {
			t.Error("expected test_tool in plan defs")
		}
	})

	t.Run("planOnly=false returns main_task tools", func(t *testing.T) {
		defs := BuildToolDefs(entries, false)
		if len(defs) != 2 {
			t.Fatalf("expected 2 defs, got %d", len(defs))
		}
	})

	t.Run("invalid definition JSON is skipped", func(t *testing.T) {
		bad := []toolsconfig.ToolConfigEntry{
			{Name: "bad", PlanTask: true, MainTask: true, Definition: json.RawMessage(`{invalid}`)},
			{Name: "good", PlanTask: true, MainTask: true, Definition: funcDef},
		}
		defs := BuildToolDefs(bad, true)
		if len(defs) != 1 {
			t.Fatalf("expected 1 def (bad skipped), got %d", len(defs))
		}
	})

	t.Run("empty entries returns nil", func(t *testing.T) {
		defs := BuildToolDefs(nil, true)
		if defs != nil {
			t.Errorf("expected nil, got %v", defs)
		}
	})
}
