package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/open-code-review/open-code-review/internal/agent"
	"github.com/open-code-review/open-code-review/internal/model"
	"github.com/open-code-review/open-code-review/internal/suggestdiff"
)

// severityOrder maps a severity string to its sort rank (lower = more severe).
var severityOrder = map[string]int{
	"high":   0,
	"medium": 1,
	"low":    2,
}

// allowedLevels lists the recognized severity values.
var allowedLevels = func() map[string]bool {
	m := make(map[string]bool, len(severityOrder))
	for k := range severityOrder {
		m[k] = true
	}
	return m
}()

// severityColors maps severity to an ANSI background + foreground color.
var severityColors = map[string]string{
	"high":   "\033[48;2;180;0;0m\033[97m",   // dark red bg + white fg
	"medium": "\033[48;2;180;120;0m\033[97m", // dark orange bg + white fg
	"low":    "\033[48;2;80;80;80m\033[97m",  // gray bg + white fg
}

// categoryOrder maps a category string to its sort rank (lower = more important).
var categoryOrder = map[string]int{
	"bug":             0,
	"security":        1,
	"performance":     2,
	"maintainability": 3,
	"improvement":     4,
	"style":           5,
	"documentation":   6,
	"other":           7,
}

// allowedCategories lists the recognized categories for validation use.
var allowedCategories = func() map[string]bool {
	m := make(map[string]bool, len(categoryOrder))
	for k := range categoryOrder {
		m[k] = true
	}
	return m
}()

// filterConfig is an immutable snapshot of the active --level and --category
// CLI filter values. Nil maps mean "allow all". Create via parseFilterFlags.
type filterConfig struct {
	levels     map[string]bool
	categories map[string]bool
}

// hasActiveFilters reports whether any filter is set.
func (f *filterConfig) hasActiveFilters() bool {
	return f != nil && (len(f.levels) > 0 || len(f.categories) > 0)
}

// parseFilterFlags parses comma-separated --level and --category values into
// a filterConfig with validation. Empty flags produce a nil config (allow all).
func parseFilterFlags(levelFlag, categoryFlag string) *filterConfig {
	levels := splitCSV(strings.ToLower(levelFlag))
	cats := splitCSV(strings.ToLower(categoryFlag))
	if len(levels) == 0 && len(cats) == 0 {
		return nil
	}

	f := &filterConfig{}
	if len(levels) > 0 {
		m := make(map[string]bool, len(levels))
		for _, l := range levels {
			if allowedLevels[l] {
				m[l] = true
			}
		}
		if len(m) > 0 {
			f.levels = m
		}
	}
	if len(cats) > 0 {
		m := make(map[string]bool, len(cats))
		for _, c := range cats {
			if allowedCategories[c] {
				m[c] = true
			}
		}
		if len(m) > 0 {
			f.categories = m
		}
	}
	if f.levels == nil && f.categories == nil {
		return nil
	}
	return f
}

// severityRank returns the sort rank for a comment.
func severityRank(c model.LlmComment) int {
	if r, ok := severityOrder[c.Severity]; ok {
		return r
	}
	return len(severityOrder) // unknown severity sorts last
}

// categoryRank returns the sort rank for a comment.
func categoryRank(c model.LlmComment) int {
	if r, ok := categoryOrder[c.Category]; ok {
		return r
	}
	return len(categoryOrder) // unknown category sorts last
}

// sortComments sorts findings by severity (high -> medium -> low), then by category.
func sortComments(comments []model.LlmComment) {
	sort.SliceStable(comments, func(i, j int) bool {
		si := severityRank(comments[i])
		sj := severityRank(comments[j])
		if si != sj {
			return si < sj
		}
		return categoryRank(comments[i]) < categoryRank(comments[j])
	})
}

// filterComments filters out findings that don't match the active level/category filters.
// A nil fc means "allow all".
// if the LLM not populated the severity or category, regard to show the comment
func filterComments(comments []model.LlmComment, fc *filterConfig) []model.LlmComment {
	if fc == nil || (!fc.hasActiveFilters()) {
		return comments
	}

	out := make([]model.LlmComment, 0, len(comments))
	for _, c := range comments {
		if len(fc.levels) > 0 && c.Severity != "" && !fc.levels[c.Severity] {
			continue
		}
		if len(fc.categories) > 0 && c.Category != "" && !fc.categories[c.Category] {
			continue
		}
		out = append(out, c)
	}
	return out
}

func outputText(comments []model.LlmComment) {
	if len(comments) == 0 {
		fmt.Println("No comments generated. Looks good to me.")
		return
	}
	for _, c := range comments {
		renderComment(c)
	}
}

func hasSubtaskErrors(warnings []agent.AgentWarning) bool {
	for _, w := range warnings {
		if w.Type == "subtask_error" {
			return true
		}
	}
	return false
}

func outputTextWithWarnings(comments []model.LlmComment, warnings []agent.AgentWarning) {
	if len(comments) == 0 {
		if hasSubtaskErrors(warnings) {
			fmt.Println("Some files could not be reviewed due to errors (see warnings below).")
		} else {
			fmt.Println("No comments generated. Looks good to me.")
		}
	} else {
		for _, c := range comments {
			renderComment(c)
		}
	}
	for _, w := range warnings {
		if w.Type == "subtask_error" {
			continue
		}
		fmt.Fprintf(os.Stderr, "[ocr] WARNING [%s] %s: %s\n", w.Type, sanitizeTerminal(w.File), sanitizeTerminal(w.Message))
	}
}

func renderComment(comment model.LlmComment) {
	lines := buildDiffLines(comment)
	if len(lines) == 0 && comment.Content == "" {
		return
	}

	fmt.Printf("\n\033[2m─── %s:%d-%d ───\033[0m\n", sanitizeTerminal(comment.Path), comment.StartLine, comment.EndLine)

	if badge := buildBadge(comment); badge != "" {
		fmt.Printf("%s\n", badge)
	}

	if comment.Content != "" {
		for _, ln := range wrapByRunes(sanitizeTerminal(comment.Content), 100) {
			fmt.Printf("%s\n", ln)
		}
		fmt.Println()
	}

	if len(lines) > 0 {
		for _, dl := range lines {
			switch dl.Type {
			case suggestdiff.DiffAdded:
				printDiffLine("+", sanitizeTerminal(dl.Content), "\033[92m", "\033[48;2;0;60;0m")
			case suggestdiff.DiffDeleted:
				printDiffLine("-", sanitizeTerminal(dl.Content), "\033[91m", "\033[48;2;70;0;0m")
			case suggestdiff.DiffContext:
				printDiffLine(" ", sanitizeTerminal(dl.Content), "\033[2m", "\033[48;2;38;38;38m")
			}
		}
	}

	fmt.Println()
}

// buildBadge renders a colored badge line for a comment, e.g.
// Returns empty string when neither severity nor category is set.
func buildBadge(comment model.LlmComment) string {
	if comment.Severity == "" && comment.Category == "" {
		return ""
	}

	var sb strings.Builder
	sb.WriteByte('[')

	color := ""
	label := comment.Severity
	if comment.Severity != "" && allowedLevels[comment.Severity] {
		color = severityColors[comment.Severity]
	}

	if label != "" {
		if color != "" {
			sb.WriteString(color)
		}
		sb.WriteString(label)
		if color != "" {
			sb.WriteString("\033[0m")
		}
	}
	if comment.Category != "" {
		if label != "" {
			sb.WriteString("·")
		}
		sb.WriteString(comment.Category)
	}
	sb.WriteByte(']')

	return sb.String()
}

// printDiffLine renders a single diff line with colored prefix and background on content.
func printDiffLine(prefix, content, fgColor, bgColor string) {
	fmt.Printf("%s%s%s %s%s\033[0m\n", fgColor+bgColor, prefix, "\033[0m"+bgColor, content, "\033[0m")
}

// wrapByRunes splits text into lines that fit within maxWidth **rune** columns.
// Respects existing newlines and wraps at word boundaries.
func wrapByRunes(text string, maxW int) []string {
	if text == "" {
		return nil
	}
	var result []string
	for _, para := range strings.Split(text, "\n") {
		result = append(result, wrapSingleRuneLine(para, maxW)...)
	}
	return result
}

// wrapSingleRuneLine breaks one paragraph (no newlines) into rune-width-constrained lines.
func wrapSingleRuneLine(line string, maxW int) []string {
	runes := []rune(line)
	if visibleRunesLen(runes) <= maxW {
		return []string{line}
	}
	var result []string
	for len(runes) > 0 {
		cut := runeWrapCut(runes, maxW)
		result = append(result, string(runes[:cut]))
		runes = runes[cut:]
		// trim leading spaces of next segment
		for len(runes) > 0 && runes[0] == ' ' {
			runes = runes[1:]
		}
	}
	return result
}

// runeWrapCut returns a rune index suitable for breaking the line at ~maxW display width.
func runeWrapCut(runes []rune, maxW int) int {
	if visibleRunesLen(runes) <= maxW {
		return len(runes)
	}
	best := maxW
	if best >= len(runes) {
		return len(runes)
	}
	for i := best; i > 0; i-- {
		if runes[i] == ' ' || runes[i] == '\t' {
			return i
		}
	}
	return best
}

func visibleRunesLen(runes []rune) int {
	n := 0
	for _, r := range runes {
		if r >= 32 && r != 127 {
			n++
		}
	}
	return n
}

func sanitizeTerminal(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r == '\t' || r == '\n' || !unicode.IsControl(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func splitToLines(s string) []string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func buildDiffLines(comment model.LlmComment) []suggestdiff.DiffLine {
	if comment.SuggestionCode == "" || comment.ExistingCode == "" {
		return nil
	}
	oldLines := splitToLines(comment.ExistingCode)
	newLines := splitToLines(comment.SuggestionCode)
	return suggestdiff.ComputeLineDiff(oldLines, newLines)
}

type jsonSummary struct {
	FilesReviewed    int64  `json:"files_reviewed"`
	Comments         int64  `json:"comments"`
	FilteredComments int64  `json:"filtered_comments,omitempty"`
	TotalTokens      int64  `json:"total_tokens"`
	InputTokens      int64  `json:"input_tokens"`
	OutputTokens     int64  `json:"output_tokens"`
	CacheReadTokens  int64  `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int64  `json:"cache_write_tokens,omitempty"`
	Elapsed          string `json:"elapsed"`
}

type jsonToolCalls struct {
	Total  int64            `json:"total"`
	ByTool map[string]int64 `json:"by_tool"`
}

type jsonOutput struct {
	Status         string               `json:"status"`
	Message        string               `json:"message,omitempty"`
	Summary        *jsonSummary         `json:"summary,omitempty"`
	ToolCalls      *jsonToolCalls       `json:"tool_calls"`
	Comments       []model.LlmComment   `json:"comments"`
	FilteredCount  int                  `json:"filtered_count,omitempty"`
	Warnings       []agent.AgentWarning `json:"warnings,omitempty"`
	ProjectSummary string               `json:"project_summary,omitempty"`
}

func outputJSON(comments []model.LlmComment, filteredCount int) error {
	out := jsonOutput{
		Status:   "success",
		Comments: comments,
	}
	if filteredCount > 0 {
		out.FilteredCount = filteredCount
	}
	if len(comments) == 0 {
		out.Message = "No comments generated. Looks good to me."
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func outputJSONWithWarnings(comments []model.LlmComment, warnings []agent.AgentWarning,
	filesReviewed, inputTokens, outputTokens, totalTokens, cacheReadTokens, cacheWriteTokens int64,
	duration time.Duration, projectSummary string, toolCalls map[string]int64,
	commentsBeforeFilter, commentsAfterFilter int) error {
	out := jsonOutput{
		Status:   "success",
		Comments: comments,
		Summary: &jsonSummary{
			FilesReviewed:    filesReviewed,
			Comments:         int64(commentsAfterFilter),
			FilteredComments: int64(commentsBeforeFilter - commentsAfterFilter),
			TotalTokens:      totalTokens,
			InputTokens:      inputTokens,
			OutputTokens:     outputTokens,
			CacheReadTokens:  cacheReadTokens,
			CacheWriteTokens: cacheWriteTokens,
			Elapsed:          duration.Round(time.Second).String(),
		},
		ProjectSummary: projectSummary,
	}
	var total int64
	for _, v := range toolCalls {
		total += v
	}
	byTool := toolCalls
	if byTool == nil {
		byTool = make(map[string]int64)
	}
	out.ToolCalls = &jsonToolCalls{
		Total:  total,
		ByTool: byTool,
	}
	if commentsAfterFilter == 0 {
		if hasSubtaskErrors(warnings) {
			out.Message = "Some files could not be reviewed due to errors."
		} else {
			out.Message = "No comments generated. Looks good to me."
		}
	}
	if len(warnings) > 0 {
		out.Warnings = warnings
		if hasSubtaskErrors(warnings) {
			out.Status = "completed_with_errors"
		} else {
			out.Status = "completed_with_warnings"
		}
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func outputJSONNoFiles() error {
	out := jsonOutput{
		Status:   "skipped",
		Message:  "No supported files changed.",
		Comments: []model.LlmComment{},
		ToolCalls: &jsonToolCalls{
			ByTool: map[string]int64{},
		},
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func outputPreviewText(p *agent.DiffPreview) {
	if p.TotalFiles == 0 {
		fmt.Println("No files changed.")
		return
	}

	maxPathLen := 0
	for _, e := range p.Entries {
		if n := len(sanitizeTerminal(e.Path)); n > maxPathLen {
			maxPathLen = n
		}
	}
	if maxPathLen < 20 {
		maxPathLen = 20
	}
	pathFmt := fmt.Sprintf("%%-%ds", maxPathLen)

	fmt.Printf("\nPreview: %d file(s) changed  |  \033[32m+%d\033[0m  \033[31m-%d\033[0m\n",
		p.TotalFiles, p.TotalInsertions, p.TotalDeletions)

	if p.ReviewableCount > 0 {
		fmt.Printf("\n\033[1mWill review (%d):\033[0m\n", p.ReviewableCount)
		for _, e := range p.Entries {
			if !e.WillReview {
				continue
			}
			fmt.Printf("  %s  "+pathFmt+" \033[32m+%-4d\033[0m \033[31m-%-4d\033[0m\n",
				statusBadge(e.Status), sanitizeTerminal(e.Path), e.Insertions, e.Deletions)
		}
	}

	if p.ExcludedCount > 0 {
		fmt.Printf("\n\033[1mExcluded from review (%d):\033[0m\n", p.ExcludedCount)
		for _, e := range p.Entries {
			if e.WillReview {
				continue
			}
			fmt.Printf("  %s  "+pathFmt+" \033[2m(%s)\033[0m\n",
				statusBadge(e.Status), sanitizeTerminal(e.Path), sanitizeTerminal(string(e.ExcludeReason)))
		}
	}

	fmt.Println()
}

func statusBadge(status string) string {
	switch status {
	case "added":
		return "\033[32m[A]\033[0m"
	case "modified":
		return "\033[33m[M]\033[0m"
	case "deleted":
		return "\033[31m[D]\033[0m"
	case "renamed":
		return "\033[36m[R]\033[0m"
	case "binary":
		return "\033[35m[B]\033[0m"
	case "scan":
		return "\033[34m[S]\033[0m"
	default:
		return "[?]"
	}
}

// splitCSV splits a comma-separated string, trimming whitespace, and
// returns non-empty entries.
func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
