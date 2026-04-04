package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/buildoak/agent-mux/internal/sanitize"
	"gopkg.in/yaml.v3"
)

type CoordinatorSpec struct {
	Name         string
	Model        string
	Effort       string
	Engine       string
	Skills       []string
	Timeout      int
	SystemPrompt string
}

func LoadProfile(name, cwd string) (*CoordinatorSpec, error) {
	name = strings.TrimSpace(name)
	if err := sanitize.ValidateBasename(name); err != nil {
		return nil, fmt.Errorf("invalid profile name %q: %w", name, err)
	}

	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("get working directory: %w", err)
		}
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("get home directory: %w", err)
	}

	searchDirs := []string{
		filepath.Join(cwd, ".agent-mux", "prompts"),
		filepath.Join(homeDir, ".agent-mux", "prompts"),
	}

	for _, dir := range searchDirs {
		path := filepath.Join(dir, name+".md")
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("stat profile %q: %w", path, err)
		}
		if info.IsDir() {
			return nil, fmt.Errorf("profile path %q is a directory", path)
		}

		spec, err := loadCoordinatorSpec(path, name)
		if err != nil {
			return nil, err
		}
		return spec, nil
	}

	available, err := availableCoordinators(searchDirs)
	if err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("profile %q not found. Available profiles: %v", name, available)
}

func loadCoordinatorSpec(path, name string) (*CoordinatorSpec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read profile %q: %w", path, err)
	}

	frontmatter, body, err := splitFrontmatter(data)
	if err != nil {
		return nil, fmt.Errorf("parse profile %q: %w", path, err)
	}

	spec := &CoordinatorSpec{
		Name:         name,
		SystemPrompt: body,
	}

	if len(frontmatter) == 0 {
		return spec, nil
	}

	var parsed struct {
		Model   string   `yaml:"model"`
		Effort  string   `yaml:"effort"`
		Engine  string   `yaml:"engine"`
		Skills  []string `yaml:"skills"`
		Timeout int      `yaml:"timeout"`
	}
	if err := yaml.Unmarshal(frontmatter, &parsed); err != nil {
		return nil, fmt.Errorf("decode frontmatter: %w", err)
	}

	var raw map[string]any
	if err := yaml.Unmarshal(frontmatter, &raw); err != nil {
		return nil, fmt.Errorf("decode frontmatter fields: %w", err)
	}
	if _, ok := raw["timeout"]; ok {
		if err := validatePositiveInt("timeout", path, parsed.Timeout); err != nil {
			return nil, err
		}
	}

	spec.Model = parsed.Model
	spec.Effort = parsed.Effort
	spec.Engine = parsed.Engine
	spec.Skills = append([]string(nil), parsed.Skills...)
	spec.Timeout = parsed.Timeout

	return spec, nil
}

func splitFrontmatter(data []byte) ([]byte, string, error) {
	content := string(data)
	if !strings.HasPrefix(content, "---") {
		return nil, content, nil
	}

	lines := strings.SplitAfter(content, "\n")
	if len(lines) == 0 || strings.TrimRight(lines[0], "\r\n") != "---" {
		return nil, content, nil
	}

	var frontmatter strings.Builder
	offset := len(lines[0])
	for i := 1; i < len(lines); i++ {
		line := lines[i]
		offset += len(line)
		if strings.TrimRight(line, "\r\n") == "---" {
			return []byte(frontmatter.String()), content[offset:], nil
		}
		frontmatter.WriteString(line)
	}

	return nil, "", fmt.Errorf("missing closing frontmatter delimiter")
}

// PromptFileInfo describes a discovered prompt/profile file for `config prompts`.
type PromptFileInfo struct {
	Name   string   `json:"name"`
	Path   string   `json:"path"`
	Source string   `json:"source"`
	Skills []string `json:"skills,omitempty"`
	Effort string   `json:"effort,omitempty"`
	Engine string   `json:"engine,omitempty"`
}

// DiscoverPromptFiles scans all profile/prompt search directories and returns
// deduplicated results with first-match-wins semantics.
func DiscoverPromptFiles(cwd string) []PromptFileInfo {
	if cwd == "" {
		cwd, _ = os.Getwd()
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = ""
	}

	type labeledDir struct {
		dir   string
		label string
	}

	var searchDirs []labeledDir
	searchDirs = append(searchDirs,
		labeledDir{filepath.Join(cwd, ".agent-mux", "prompts"), "project"},
	)
	if homeDir != "" {
		searchDirs = append(searchDirs,
			labeledDir{filepath.Join(homeDir, ".agent-mux", "prompts"), "global"},
		)
	}

	seen := make(map[string]struct{})
	var results []PromptFileInfo

	for _, sd := range searchDirs {
		entries, err := os.ReadDir(sd.dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			fname := entry.Name()
			if filepath.Ext(fname) != ".md" {
				continue
			}
			name := strings.TrimSuffix(fname, ".md")
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}

			fullPath := filepath.Join(sd.dir, fname)
			info := PromptFileInfo{
				Name:   name,
				Path:   fullPath,
				Source: sd.label,
			}

			// Try to parse frontmatter for metadata.
			if spec, err := loadCoordinatorSpec(fullPath, name); err == nil {
				info.Skills = spec.Skills
				info.Effort = spec.Effort
				info.Engine = spec.Engine
			}

			results = append(results, info)
		}
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Name < results[j].Name
	})
	return results
}

func availableCoordinators(dirs []string) ([]string, error) {
	seen := map[string]struct{}{}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("read profile directory %q: %w", dir, err)
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			if filepath.Ext(name) != ".md" {
				continue
			}
			seen[strings.TrimSuffix(name, ".md")] = struct{}{}
		}
	}

	available := make([]string, 0, len(seen))
	for name := range seen {
		available = append(available, name)
	}
	sort.Strings(available)
	return available, nil
}
