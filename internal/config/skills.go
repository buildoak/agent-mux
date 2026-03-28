package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/buildoak/agent-mux/internal/sanitize"
)

// LoadSkills loads skill SKILL.md files by name and returns a concatenated prompt
// block and a list of scripts directories to add to PATH.
func LoadSkills(names []string, cwd string) (prompt string, pathDirs []string, err error) {
	if len(names) == 0 {
		return "", nil, nil
	}

	skillsRoot := filepath.Join(cwd, ".claude", "skills")
	seen := make(map[string]struct{}, len(names))
	blocks := make([]string, 0, len(names))
	pathDirs = make([]string, 0)

	for _, name := range names {
		name = strings.TrimSpace(name)
		if err := sanitize.ValidateBasename(name); err != nil {
			return "", nil, fmt.Errorf("invalid skill name %q: %w", name, err)
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}

		skillFile := filepath.Join(skillsRoot, name, "SKILL.md")
		content, readErr := os.ReadFile(skillFile)
		if readErr != nil {
			if os.IsNotExist(readErr) {
				return "", nil, fmt.Errorf("skill %q not found at %q. Available skills: %v", name, skillFile, availableSkills(skillsRoot))
			}
			return "", nil, fmt.Errorf("read skill %q: %w", name, readErr)
		}

		trimmed := strings.TrimRight(string(content), "\r\n")
		block := fmt.Sprintf("<skill name=%q>\n%s\n</skill>\n", name, trimmed)
		blocks = append(blocks, block)

		scriptsDir := filepath.Join(skillsRoot, name, "scripts")
		info, statErr := os.Stat(scriptsDir)
		if statErr == nil && info.IsDir() {
			pathDirs = append(pathDirs, scriptsDir)
		} else if statErr != nil && !os.IsNotExist(statErr) {
			return "", nil, fmt.Errorf("stat scripts dir for skill %q: %w", name, statErr)
		}
	}

	return strings.Join(blocks, "\n"), pathDirs, nil
}

func availableSkills(skillsRoot string) []string {
	entries, err := os.ReadDir(skillsRoot)
	if err != nil {
		return nil
	}

	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			out = append(out, entry.Name())
		}
	}
	sort.Strings(out)
	return out
}
