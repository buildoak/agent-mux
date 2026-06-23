package config

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const agyModelCacheVersion = 1

var agyModelsRefreshTimeout = 5 * time.Second

type AgyModelState struct {
	Models    []string `json:"models"`
	Source    string   `json:"source"`
	Status    string   `json:"status"`
	CachePath string   `json:"cache_path,omitempty"`
}

type agyModelCacheFile struct {
	Version     int       `json:"version"`
	Source      string    `json:"source"`
	Status      string    `json:"status"`
	Models      []string  `json:"models"`
	RefreshedAt time.Time `json:"refreshed_at"`
}

// AgyModelCachePath returns the on-disk cache path for dynamic agy models.
func AgyModelCachePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(home) == "" {
		return "", errors.New("empty home directory")
	}
	return filepath.Join(home, ".agent-mux", "cache", "agy-models.json"), nil
}

// ModelsWithCachedAgy returns deterministic built-ins overlaid with a valid
// agy model cache when one is present. It never invokes the agy CLI.
func ModelsWithCachedAgy() map[string][]string {
	models := DefaultModels()
	state := CachedAgyModelState()
	if state.Source == "cache" && len(state.Models) > 0 {
		models["agy"] = cloneStrings(state.Models)
	}
	return models
}

// CachedAgyModelState returns cached agy models when a valid cache exists,
// otherwise the built-in fallback state. It never invokes the agy CLI.
func CachedAgyModelState() AgyModelState {
	cachePath, err := AgyModelCachePath()
	if err != nil {
		return fallbackAgyModelState("cache_unavailable")
	}
	state, err := readAgyModelCache(cachePath)
	if err != nil {
		return fallbackAgyModelState("fallback")
	}
	state.CachePath = cachePath
	return state
}

// RefreshAgyModelCache runs `agy models` with a short timeout, parses model
// names, and writes the cache. Normal config/dispatch paths should not call it.
func RefreshAgyModelCache() (AgyModelState, error) {
	cachePath, pathErr := AgyModelCachePath()
	ctx, cancel := context.WithTimeout(context.Background(), agyModelsRefreshTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "agy", "models")
	output, err := cmd.Output()
	if ctx.Err() == context.DeadlineExceeded {
		state := fallbackAgyModelState("refresh_timeout")
		state.CachePath = cachePath
		return state, fmt.Errorf("agy models timed out after %s", agyModelsRefreshTimeout)
	}
	if err != nil {
		state := fallbackAgyModelState("refresh_failed")
		state.CachePath = cachePath
		return state, fmt.Errorf("agy models failed: %w", err)
	}

	models := ParseAgyModelNames(output)
	if len(models) == 0 {
		state := fallbackAgyModelState("refresh_empty")
		state.CachePath = cachePath
		return state, fmt.Errorf("agy models returned no parseable model names")
	}
	if pathErr != nil {
		state := AgyModelState{
			Models: cloneStrings(models),
			Source: "agy_models",
			Status: "refresh_cache_unavailable",
		}
		return state, pathErr
	}
	if err := writeAgyModelCache(cachePath, models); err != nil {
		state := AgyModelState{
			Models:    cloneStrings(models),
			Source:    "agy_models",
			Status:    "refresh_cache_write_failed",
			CachePath: cachePath,
		}
		return state, err
	}
	return AgyModelState{
		Models:    cloneStrings(models),
		Source:    "agy_models",
		Status:    "refreshed",
		CachePath: cachePath,
	}, nil
}

// ParseAgyModelNames extracts model names from common `agy models` output
// shapes: JSON arrays/objects and simple text or bullet lists.
func ParseAgyModelNames(output []byte) []string {
	trimmed := bytes.TrimSpace(output)
	if len(trimmed) == 0 {
		return nil
	}
	if models := parseAgyModelJSON(trimmed); len(models) > 0 {
		return filterAgyModelNames(models)
	}

	var models []string
	scanner := bufio.NewScanner(bytes.NewReader(trimmed))
	for scanner.Scan() {
		name := cleanAgyModelLine(scanner.Text())
		if name == "" {
			continue
		}
		models = append(models, name)
	}
	return filterAgyModelNames(models)
}

func parseAgyModelJSON(output []byte) []string {
	var raw any
	if err := json.Unmarshal(output, &raw); err != nil {
		return nil
	}
	return extractAgyModelNames(raw)
}

func extractAgyModelNames(raw any) []string {
	switch v := raw.(type) {
	case []any:
		var out []string
		for _, item := range v {
			out = append(out, extractAgyModelNames(item)...)
		}
		return out
	case map[string]any:
		for _, key := range []string{"models", "data", "items"} {
			if nested, ok := v[key]; ok {
				if names := extractAgyModelNames(nested); len(names) > 0 {
					return names
				}
			}
		}
		for _, key := range []string{"name", "display_name", "displayName", "model", "id"} {
			if s, ok := v[key].(string); ok {
				if name := strings.TrimSpace(s); name != "" {
					return []string{name}
				}
			}
		}
	case string:
		if name := strings.TrimSpace(v); name != "" {
			return []string{name}
		}
	}
	return nil
}

func cleanAgyModelLine(line string) string {
	line = strings.TrimSpace(line)
	if line == "" {
		return ""
	}
	line = strings.Trim(line, "`")
	line = strings.TrimSpace(line)
	lower := strings.ToLower(line)
	if lower == "models" || lower == "models:" || lower == "available models" || lower == "available models:" {
		return ""
	}
	if strings.HasPrefix(lower, "name ") || strings.HasPrefix(lower, "name\t") || strings.HasPrefix(lower, "name|") {
		return ""
	}

	line = strings.TrimLeft(line, "-*• \t")
	line = strings.TrimSpace(line)
	line = trimOrderedPrefix(line)
	if idx := strings.Index(line, "|"); idx >= 0 {
		line = strings.TrimSpace(line[:idx])
	}
	if idx := strings.Index(line, "\t"); idx >= 0 {
		line = strings.TrimSpace(line[:idx])
	}
	return strings.TrimSpace(line)
}

func filterAgyModelNames(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	var models []string
	for _, item := range items {
		name := strings.TrimSpace(item)
		if !isLikelyAgyModelName(name) {
			continue
		}
		models = append(models, name)
	}
	return deduplicateStrings(models)
}

func isLikelyAgyModelName(name string) bool {
	if name == "" || len(name) > 120 {
		return false
	}
	lower := strings.ToLower(name)
	rejectFragments := []string{
		"error", "failed", "failure", "unauthorized", "forbidden",
		"authenticate", "authentication", "login", "log in",
		"usage:", "command not found", "not configured",
	}
	for _, fragment := range rejectFragments {
		if strings.Contains(lower, fragment) {
			return false
		}
	}
	if strings.ContainsAny(name, "\r\n") || strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return false
	}

	hasLetter := false
	hasDigit := false
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			hasLetter = true
		}
		if r >= '0' && r <= '9' {
			hasDigit = true
		}
	}
	if !hasLetter {
		return false
	}
	if hasDigit {
		return true
	}
	for _, marker := range []string{
		"gemini", "claude", "gpt", "openai", "anthropic",
		"flash", "pro", "sonnet", "opus", "haiku",
		"nano", "banana",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func trimOrderedPrefix(line string) string {
	i := 0
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	if i == 0 || i >= len(line) {
		return line
	}
	switch line[i] {
	case '.', ')':
		return strings.TrimSpace(line[i+1:])
	}
	return line
}

func readAgyModelCache(path string) (AgyModelState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return AgyModelState{}, err
	}
	var cache agyModelCacheFile
	if err := json.Unmarshal(data, &cache); err != nil {
		return AgyModelState{}, err
	}
	if cache.Version != agyModelCacheVersion {
		return AgyModelState{}, fmt.Errorf("agy model cache version %d unsupported", cache.Version)
	}
	if cache.Source != "agy_models" || cache.Status != "ok" {
		return AgyModelState{}, fmt.Errorf("agy model cache source/status invalid: %s/%s", cache.Source, cache.Status)
	}
	if cache.RefreshedAt.IsZero() {
		return AgyModelState{}, errors.New("agy model cache missing refreshed_at")
	}
	models := filterAgyModelNames(cache.Models)
	if len(models) == 0 {
		return AgyModelState{}, errors.New("agy model cache has no models")
	}
	return AgyModelState{
		Models: cloneStrings(models),
		Source: "cache",
		Status: "ok",
	}, nil
}

func writeAgyModelCache(path string, models []string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	models = filterAgyModelNames(models)
	if len(models) == 0 {
		return errors.New("agy model cache has no valid models")
	}
	cache := agyModelCacheFile{
		Version:     agyModelCacheVersion,
		Source:      "agy_models",
		Status:      "ok",
		Models:      models,
		RefreshedAt: time.Now().UTC(),
	}
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

func fallbackAgyModelState(status string) AgyModelState {
	if status == "" {
		status = "fallback"
	}
	return AgyModelState{
		Models: cloneStrings(DefaultModels()["agy"]),
		Source: "built_in",
		Status: status,
	}
}
