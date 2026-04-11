package skillindex

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Frontmatter is YAML between --- lines at the top of SKILL.md.
type Frontmatter struct {
	Name      string   `yaml:"name"`
	Title     string   `yaml:"title"`
	DependsOn []string `yaml:"depends_on"`
	Tools     []string `yaml:"tools"`
}

// ParseSkillMarkdown splits frontmatter and markdown body.
func ParseSkillMarkdown(raw string) (Frontmatter, string, error) {
	var fm Frontmatter
	s := strings.TrimPrefix(raw, "\ufeff")
	if !strings.HasPrefix(s, "---") {
		return fm, strings.TrimSpace(s), nil
	}
	rest := strings.TrimPrefix(s, "---")
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return fm, strings.TrimSpace(s), fmt.Errorf("skill: missing closing --- for frontmatter")
	}
	yamlBlock := strings.TrimSpace(rest[:idx])
	body := strings.TrimSpace(rest[idx+4:])
	if err := yaml.Unmarshal([]byte(yamlBlock), &fm); err != nil {
		return fm, body, fmt.Errorf("skill frontmatter: %w", err)
	}
	return fm, body, nil
}
