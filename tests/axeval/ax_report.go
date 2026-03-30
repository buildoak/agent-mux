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

// ── AX Report (new tier-based) ─────────────────────────────────────────

// AXReport is the JSON structure for the tier-based report.
type AXReport struct {
	RunID     string          `json:"run_id"`
	Timestamp string          `json:"timestamp"`
	Verdicts  []AXVerdict     `json:"verdicts"`
	Summary   AXReportSummary `json:"summary"`
}

// AXReportSummary provides aggregate stats across tiers.
type AXReportSummary struct {
	Total       int                  `json:"total"`
	Passed      int                  `json:"passed"`
	Failed      int                  `json:"failed"`
	AXHealthPct float64              `json:"ax_health_pct"`
	ByTier      map[Tier]AXTierStats `json:"by_tier"`
}

// AXTierStats tracks pass/fail for a single tier.
type AXTierStats struct {
	Total  int     `json:"total"`
	Passed int     `json:"passed"`
	Failed int     `json:"failed"`
	AvgScore float64 `json:"avg_score"`
}

// axReportCollector accumulates AX verdicts from parallel test runs.
type axReportCollector struct {
	mu       sync.Mutex
	verdicts []AXVerdict
}

var axCollector = &axReportCollector{}

// recordAXVerdict adds an AX verdict to the collector.
func recordAXVerdict(v AXVerdict) {
	axCollector.mu.Lock()
	defer axCollector.mu.Unlock()
	axCollector.verdicts = append(axCollector.verdicts, v)
}

// writeAXReport writes the collected AX verdicts to AX_EVAL_REPORT_DIR/ax-report.json.
func writeAXReport() error {
	dir := os.Getenv("AX_EVAL_REPORT_DIR")
	if dir == "" {
		return nil
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create report dir: %w", err)
	}

	axCollector.mu.Lock()
	verdicts := make([]AXVerdict, len(axCollector.verdicts))
	copy(verdicts, axCollector.verdicts)
	axCollector.mu.Unlock()

	summary := AXReportSummary{
		Total:  len(verdicts),
		ByTier: make(map[Tier]AXTierStats),
	}

	for _, v := range verdicts {
		if v.Pass {
			summary.Passed++
		} else {
			summary.Failed++
		}
		ts := summary.ByTier[v.Tier]
		ts.Total++
		ts.AvgScore += v.Score
		if v.Pass {
			ts.Passed++
		} else {
			ts.Failed++
		}
		summary.ByTier[v.Tier] = ts
	}

	// Compute averages.
	for tier, ts := range summary.ByTier {
		if ts.Total > 0 {
			ts.AvgScore /= float64(ts.Total)
		}
		summary.ByTier[tier] = ts
	}

	if summary.Total > 0 {
		summary.AXHealthPct = float64(summary.Passed) / float64(summary.Total) * 100
	}

	report := AXReport{
		RunID:     fmt.Sprintf("ax-%d", time.Now().Unix()),
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Verdicts:  verdicts,
		Summary:   summary,
	}

	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal ax report: %w", err)
	}

	path := filepath.Join(dir, "ax-report.json")
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write ax report: %w", err)
	}

	fmt.Fprintf(os.Stderr, "ax-eval: AX report written to %s\n", path)
	return nil
}

// printAXSummary prints the AX health summary to the test log.
func printAXSummary(verdicts []AXVerdict) string {
	byTier := make(map[Tier][]AXVerdict)
	for _, v := range verdicts {
		byTier[v.Tier] = append(byTier[v.Tier], v)
	}

	var b []byte
	b = append(b, "\n=== AX Health Summary ===\n"...)

	for _, tier := range []Tier{TierL0, TierL1, TierL2, TierL3, TierL4} {
		tvs := byTier[tier]
		if len(tvs) == 0 {
			continue
		}

		passed := 0
		totalScore := 0.0
		for _, v := range tvs {
			if v.Pass {
				passed++
			}
			totalScore += v.Score
		}
		avgScore := totalScore / float64(len(tvs))

		b = append(b, fmt.Sprintf("\n%s: %d/%d passed (avg score: %.2f)\n", tier, passed, len(tvs), avgScore)...)
		for _, v := range tvs {
			status := "PASS"
			if !v.Pass {
				status = "FAIL"
			}
			b = append(b, fmt.Sprintf("  [%s] %s: score=%.2f — %s\n", status, v.CaseName, v.Score, v.Reason)...)
		}
	}

	totalPassed := 0
	for _, v := range verdicts {
		if v.Pass {
			totalPassed++
		}
	}
	if len(verdicts) > 0 {
		b = append(b, fmt.Sprintf("\nOverall AX Health: %d/%d (%.0f%%)\n",
			totalPassed, len(verdicts),
			float64(totalPassed)/float64(len(verdicts))*100)...)
	}

	return string(b)
}

// ── Legacy Report (kept for L4 backward compat) ────────────────────

// Report is the JSON structure written to AX_EVAL_REPORT_DIR/report.json.
type Report struct {
	RunID     string        `json:"run_id"`
	Timestamp string        `json:"timestamp"`
	Cases     []CaseResult  `json:"cases"`
	Summary   ReportSummary `json:"summary"`
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
	Total      int                   `json:"total"`
	Passed     int                   `json:"passed"`
	Failed     int                   `json:"failed"`
	ByCategory map[Category]CatStats `json:"by_category"`
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
