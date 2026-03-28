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
//
// Skills are resolved relative to cwd (<cwd>/.claude/skills/<name>/SKILL.md).
// If configDir is non-empty and a skill is not found relative to cwd, resolution
// falls back to configDir (<configDir>/.claude/skills/<name>/SKILL.md). This
// handles the case where a role defines skills in its config directory but the
// dispatch cwd is a different project directory.
func LoadSkills(names []string, cwd string, configDir string) (prompt string, pathDirs []string, err error) {
	if len(names) == 0 {
		return "", nil, nil
	}

	skillsRoot := filepath.Join(cwd, ".claude", "skills")

	// Fallback skills root: use configDir when it differs from cwd.
	var fallbackSkillsRoot string
	if configDir != "" && configDir != cwd {
		fallbackSkillsRoot = filepath.Join(configDir, ".claude", "skills")
	}

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

		// Try primary location (cwd-relative).
		resolvedRoot := skillsRoot
		skillFile := filepath.Join(resolvedRoot, name, "SKILL.md")
		content, readErr := os.ReadFile(skillFile)
		if readErr != nil && os.IsNotExist(readErr) && fallbackSkillsRoot != "" {
			// Primary not found — try fallback (configDir-relative).
			fallbackSkillFile := filepath.Join(fallbackSkillsRoot, name, "SKILL.md")
			fallbackContent, fallbackErr := os.ReadFile(fallbackSkillFile)
			if fallbackErr == nil {
				// Resolved via fallback.
				resolvedRoot = fallbackSkillsRoot
				skillFile = fallbackSkillFile
				content = fallbackContent
				readErr = nil
			}
		}
		if readErr != nil {
			if os.IsNotExist(readErr) {
				// Report available skills from all searched roots.
				avail := availableSkillsMulti(skillsRoot, fallbackSkillsRoot)
				return "", nil, fmt.Errorf("skill %q not found at %q. Available skills: %v", name, skillFile, avail)
			}
			return "", nil, fmt.Errorf("read skill %q: %w", name, readErr)
		}

		trimmed := strings.TrimRight(string(content), "\r\n")
		block := fmt.Sprintf("<skill name=%q>\n%s\n</skill>\n", name, trimmed)
		blocks = append(blocks, block)

		scriptsDir := filepath.Join(resolvedRoot, name, "scripts")
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
	return availableSkillsMulti(skillsRoot, "")
}

// availableSkillsMulti returns a deduplicated, sorted list of skill names found
// under primary and (optionally) fallback skills roots.
func availableSkillsMulti(primary, fallback string) []string {
	seen := make(map[string]struct{})
	collectSkills(primary, seen)
	if fallback != "" {
		collectSkills(fallback, seen)
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func collectSkills(skillsRoot string, seen map[string]struct{}) {
	entries, err := os.ReadDir(skillsRoot)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() {
			seen[entry.Name()] = struct{}{}
		}
	}
}
