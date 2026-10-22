package board

import (
	"os"
	"path/filepath"
	"strings"
)

// ConfigDir is your own crew config, kept out of every repo: $CREW_CONFIG_DIR or ~/.crew. It holds
// env (secrets, KEY=VALUE lines) and skills/<name>/SKILL.md, Claude Code skills for crew's agents.
func ConfigDir() string {
	if d := os.Getenv("CREW_CONFIG_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".crew")
}

// SkillsDir holds the skills crew gives its agents.
func SkillsDir() string { return filepath.Join(ConfigDir(), "skills") }

// skillsExclude keeps the links out of the repo's commits (see gitExclude).
const skillsExclude = ".claude/skills/"

// linkSkills links each skill in SkillsDir into repo's .claude/skills, where Claude Code finds
// project skills, so crew's agents there get them and your own sessions elsewhere don't. A skill
// the repo has of its own (not one of our links) wins. Links to skills since removed go.
func linkSkills(repo string) {
	src := SkillsDir()
	dst := filepath.Join(repo, ".claude", "skills")
	entries, _ := os.ReadDir(src)
	want := map[string]bool{}
	for _, e := range entries {
		if _, err := os.Stat(filepath.Join(src, e.Name(), "SKILL.md")); err == nil {
			want[e.Name()] = true
		}
	}
	// drop our stale links
	existing, _ := os.ReadDir(dst)
	for _, e := range existing {
		link := filepath.Join(dst, e.Name())
		if target, err := os.Readlink(link); err == nil && strings.HasPrefix(target, src+string(filepath.Separator)) && !want[e.Name()] {
			os.Remove(link)
		}
	}
	if len(want) == 0 {
		return
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return
	}
	for name := range want {
		link, target := filepath.Join(dst, name), filepath.Join(src, name)
		if cur, err := os.Readlink(link); err == nil {
			if cur == target {
				continue
			}
			if !strings.HasPrefix(cur, src+string(filepath.Separator)) {
				continue // a link someone else made
			}
			os.Remove(link)
		} else if _, err := os.Lstat(link); err == nil {
			continue // the repo's own skill
		}
		os.Symlink(target, link)
	}
	gitExclude(repo, skillsExclude)
}
