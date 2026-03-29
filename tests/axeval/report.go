//go:build axeval

package axeval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Report is the JSON structure written to AX_EVAL_REPORT_DIR/report.json.
type Report struct {
	RunID     string         `json:"run_id"`
	Timestamp string         `json:"timestamp"`
	Cases     []CaseResult   `json:"cases"`
	Summary   ReportSummary  `json:"summary"`
}

// CaseResult records the outcome of a single test case.
type CaseResult struct {
	Name       string   `json:"name"`
	Category   Category `json:"category"`
	Pass       bool     `json:"pass"`
	Score      float64  `json:"score"`
	Reason     string   `json:"reason"`
	DurationMS int64    `json:"duration_ms"`
	Events     []string `json:"events,omitempty"`
	JudgePass  *bool    `json:"judge_pass,omitempty"`
	JudgeScore *float64 `json:"judge_score,omitempty"`
}

// ReportSummary provides aggregate pass/fail counts by category.
type ReportSummary struct {
	Total      int                    `json:"total"`
	Passed     int                    `json:"passed"`
	Failed     int                    `json:"failed"`
	ByCategory map[Category]CatStats  `json:"by_category"`
}

// CatStats tracks pass/fail for a single category.
type CatStats struct {
	Total  int `json:"total"`
	Passed int `json:"passed"`
	Failed int `json:"failed"`
}

// reportCollector accumulates results from parallel test runs.
type reportCollector struct {
	mu      sync.Mutex
	results []CaseResult
}

var collector = &reportCollector{}

// recordResult adds a case result to the collector.
func recordResult(cr CaseResult) {
	collector.mu.Lock()
	defer collector.mu.Unlock()
	collector.results = append(collector.results, cr)
}

// writeReport writes the collected results to AX_EVAL_REPORT_DIR/report.json.
// Call from TestMain after m.Run().
func writeReport() error {
	dir := os.Getenv("AX_EVAL_REPORT_DIR")
	if dir == "" {
		return nil // No report requested.
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create report dir: %w", err)
	}

	collector.mu.Lock()
	results := make([]CaseResult, len(collector.results))
	copy(results, collector.results)
	collector.mu.Unlock()

	summary := ReportSummary{
		Total:      len(results),
		ByCategory: make(map[Category]CatStats),
	}
	for _, r := range results {
		if r.Pass {
			summary.Passed++
		} else {
			summary.Failed++
		}
		cat := summary.ByCategory[r.Category]
		cat.Total++
		if r.Pass {
			cat.Passed++
		} else {
			cat.Failed++
		}
		summary.ByCategory[r.Category] = cat
	}

	report := Report{
		RunID:     fmt.Sprintf("axeval-%d", time.Now().Unix()),
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Cases:     results,
		Summary:   summary,
	}

	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal report: %w", err)
	}

	path := filepath.Join(dir, "report.json")
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write report: %w", err)
	}

	return nil
}
