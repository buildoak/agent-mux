package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
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
	ExtraFields  map[string]any
}

func LoadCoordinator(name, cwd string) (*CoordinatorSpec, *Config, error) {
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return nil, nil, fmt.Errorf("get working directory: %w", err)
		}
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, nil, fmt.Errorf("get home directory: %w", err)
	}

	searchDirs := []string{
		filepath.Join(cwd, ".claude", "agents"),
		filepath.Join(homeDir, ".config", "agent-mux", "agents"),
	}

	for _, dir := range searchDirs {
		path := filepath.Join(dir, name+".md")
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, nil, fmt.Errorf("stat coordinator %q: %w", path, err)
		}
		if info.IsDir() {
			return nil, nil, fmt.Errorf("coordinator path %q is a directory", path)
		}

		spec, err := loadCoordinatorSpec(path, name)
		if err != nil {
			return nil, nil, err
		}
		companionCfg, err := loadCoordinatorCompanionConfig(filepath.Join(dir, name+".toml"))
		if err != nil {
			return nil, nil, err
		}
		return spec, companionCfg, nil
	}

	available, err := availableCoordinators(searchDirs)
	if err != nil {
		return nil, nil, err
	}
	return nil, nil, fmt.Errorf("coordinator %q not found. Available coordinators: %v", name, available)
}

func loadCoordinatorSpec(path, name string) (*CoordinatorSpec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read coordinator %q: %w", path, err)
	}

	frontmatter, body, err := splitFrontmatter(data)
	if err != nil {
		return nil, fmt.Errorf("parse coordinator %q: %w", path, err)
	}

	spec := &CoordinatorSpec{
		Name:         name,
		SystemPrompt: body,
		ExtraFields:  map[string]any{},
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

	var extra map[string]any
	if err := yaml.Unmarshal(frontmatter, &extra); err != nil {
		return nil, fmt.Errorf("decode frontmatter extra fields: %w", err)
	}
	delete(extra, "model")
	delete(extra, "effort")
	delete(extra, "engine")
	delete(extra, "skills")
	delete(extra, "timeout")

	spec.Model = parsed.Model
	spec.Effort = parsed.Effort
	spec.Engine = parsed.Engine
	spec.Skills = append([]string(nil), parsed.Skills...)
	spec.Timeout = parsed.Timeout
	spec.ExtraFields = extra

	return spec, nil
}

func loadCoordinatorCompanionConfig(path string) (*Config, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("stat coordinator companion config %q: %w", path, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("coordinator companion config %q is a directory", path)
	}

	var cfg Config
	meta, err := toml.DecodeFile(path, &cfg)
	if err != nil {
		return nil, fmt.Errorf("decode coordinator companion config %q: %w", path, err)
	}
	cfg.meta = &meta
	return &cfg, nil
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

func availableCoordinators(dirs []string) ([]string, error) {
	seen := map[string]struct{}{}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("read coordinator directory %q: %w", dir, err)
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
