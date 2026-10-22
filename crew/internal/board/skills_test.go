package board

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Skills in ~/.crew/skills reach the slot's repos as links, kept out of git; the repo's own win
func TestLinkSkills(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CREW_CONFIG_DIR", cfg)
	skill := func(name string) {
		os.MkdirAll(filepath.Join(cfg, "skills", name), 0o755)
		os.WriteFile(filepath.Join(cfg, "skills", name, "SKILL.md"), []byte("---\nname: "+name+"\n---\n"), 0o644)
	}
	skill("find-bugs")
	skill("unslop")
	os.MkdirAll(filepath.Join(cfg, "skills", "notes"), 0o755) // no SKILL.md: not a skill

	repo := t.TempDir()
	git(t, repo, "init", "-q")
	own := filepath.Join(repo, ".claude", "skills", "unslop")
	os.MkdirAll(own, 0o755)
	os.WriteFile(filepath.Join(own, "SKILL.md"), []byte("the repo's own"), 0o644)

	linkSkills(repo)
	link := filepath.Join(repo, ".claude", "skills", "find-bugs")
	if target, err := os.Readlink(link); err != nil || target != filepath.Join(cfg, "skills", "find-bugs") {
		t.Fatalf("find-bugs: %q %v", target, err)
	}
	if raw, _ := os.ReadFile(filepath.Join(own, "SKILL.md")); string(raw) != "the repo's own" {
		t.Fatal("replaced the repo's own skill")
	}
	if _, err := os.Lstat(filepath.Join(repo, ".claude", "skills", "notes")); err == nil {
		t.Fatal("linked a folder without SKILL.md")
	}
	if raw, _ := os.ReadFile(filepath.Join(repo, ".git", "info", "exclude")); !strings.Contains(string(raw), ".claude/skills/") {
		t.Fatalf("exclude: %s", raw)
	}
	if out := git(t, repo, "status", "--porcelain"); strings.Contains(out, "find-bugs") {
		t.Fatalf("the link shows in git: %s", out)
	}

	// a skill removed from ~/.crew goes from the repo too; running again changes nothing else
	os.RemoveAll(filepath.Join(cfg, "skills", "find-bugs"))
	linkSkills(repo)
	if _, err := os.Lstat(link); err == nil {
		t.Fatal("stale link kept")
	}
	if _, err := os.Stat(filepath.Join(own, "SKILL.md")); err != nil {
		t.Fatal("removed the repo's own skill")
	}
}
