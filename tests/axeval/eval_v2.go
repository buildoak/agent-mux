//go:build axeval

package axeval

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func parseJSONObject(data []byte, source string) (map[string]any, error) {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse %s as JSON object: %w", source, err)
	}
	if raw == nil {
		return nil, fmt.Errorf("%s decoded to nil object", source)
	}
	return raw, nil
}

func parseNDJSONObjects(data []byte, source string) ([]map[string]any, error) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)

	var out []map[string]any
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		obj, err := parseJSONObject(line, fmt.Sprintf("%s line %d", source, lineNo))
		if err != nil {
			return nil, err
		}
		out = append(out, obj)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan %s: %w", source, err)
	}
	return out, nil
}

func stdoutJSONObject(r Result) (map[string]any, error) {
	return parseJSONObject(r.RawStdout, "stdout")
}

func artifactJSONObject(r Result, filename string) (map[string]any, error) {
	path := filepath.Join(r.ArtifactDir, filename)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return parseJSONObject(data, path)
}

func jsonStringField(obj map[string]any, key string) (string, bool) {
	value, ok := obj[key].(string)
	return value, ok
}

func jsonObjectField(obj map[string]any, key string) (map[string]any, error) {
	value, ok := obj[key].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s missing or not an object", key)
	}
	return value, nil
}

func requireNonEmptyStringField(obj map[string]any, key string) error {
	value, ok := jsonStringField(obj, key)
	if !ok || strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s missing or empty", key)
	}
	return nil
}

func requireExactStringField(obj map[string]any, key, want string) error {
	value, ok := jsonStringField(obj, key)
	if !ok {
		return fmt.Errorf("%s missing or not a string", key)
	}
	if value != want {
		return fmt.Errorf("%s=%q, want %q", key, value, want)
	}
	return nil
}

func requirePositiveNumberField(obj map[string]any, key string) error {
	value, ok := obj[key].(float64)
	if !ok {
		return fmt.Errorf("%s missing or not a number", key)
	}
	if value <= 0 {
		return fmt.Errorf("%s=%v, want > 0", key, value)
	}
	return nil
}

func requireBoolField(obj map[string]any, key string, want bool) error {
	value, ok := obj[key].(bool)
	if !ok {
		return fmt.Errorf("%s missing or not a bool", key)
	}
	if value != want {
		return fmt.Errorf("%s=%v, want %v", key, value, want)
	}
	return nil
}

func requirePresentKeys(obj map[string]any, keys ...string) error {
	for _, key := range keys {
		if _, ok := obj[key]; !ok {
			return fmt.Errorf("%s missing", key)
		}
	}
	return nil
}

func fullOutputPathFromJSONObject(obj map[string]any) (string, error) {
	for _, key := range []string{"full_output_path", "full_output"} {
		value, ok := jsonStringField(obj, key)
		if ok && strings.TrimSpace(value) != "" {
			return value, nil
		}
	}
	return "", fmt.Errorf("full_output_path/full_output missing or empty")
}
