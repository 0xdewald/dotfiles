package board

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Each slot gets Claude Code permission rules in its repos' .claude/settings.local.json (read from
// the repo root, also by background sessions): its agent may read and edit its own slot without
// asking, and may not read (and so not edit) any other slot. Deny beats allow in every permission
// mode, so the walls hold in auto mode too. Rules anyone else put in the file are kept.

// Git rules for every slot. Background sessions normally move into their own worktree before
// editing; crew turns that off (each slot already is the agent's own checkout, on its agent/
// branch), and a session editing a checkout it didn't isolate asks before committing, which nobody
// can answer in the background. So: commit and push agent/ branches without asking; never push to
// the main branches or force-push (deny holds in every permission mode).
var gitAllow = []string{
	"Bash(git add *)",
	"Bash(git commit *)",
	"Bash(git push origin agent/*)",
	"Bash(git push -u origin agent/*)",
}
var gitDeny = []string{
	"Bash(git push * main)", "Bash(git push * main:*)",
	"Bash(git push * master)", "Bash(git push * master:*)",
	"Bash(git push * develop)", "Bash(git push * develop:*)",
	"Bash(git push --force*)", "Bash(git push -f*)", "Bash(git push * --force*)", "Bash(git push * -f*)",
}

// crew's own MCP tools (ask_decision, report_done, …), which every agent gets: no prompt for them,
// since a background agent has nobody to answer one.
var mcpAllow = []string{"mcp__crew__*"}

// PermissionsFile is where the rules go, inside each of a slot's repos.
const PermissionsFile = ".claude/settings.local.json"

// pathRule is Claude's absolute-path form: //Users/me/agents/slot-one/** (a single leading
// slash would be relative to the settings file).
func pathRule(tool, dir string) string {
	return tool + "(//" + strings.TrimPrefix(filepath.Clean(dir), "/") + "/**)"
}

// slotRepos are the checkouts a slot's agents run in: the slot itself, or the repos it holds.
func slotRepos(dir string) []string {
	if isRepo(dir) {
		return []string{dir}
	}
	var repos []string
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if p := filepath.Join(dir, e.Name()); e.IsDir() && isRepo(p) {
			repos = append(repos, p)
		}
	}
	return repos
}

// WriteSlotPermissions (re)writes the rules for every slot, so each one walls off all the others.
// Returns the files written.
func (b *Board) WriteSlotPermissions() ([]string, error) {
	slots, err := b.Slots()
	if err != nil {
		return nil, err
	}
	var crewRule = func(r string) bool { // a rule crew wrote for some slot
		if slices.Contains(gitAllow, r) || slices.Contains(gitDeny, r) || slices.Contains(mcpAllow, r) {
			return true
		}
		for _, s := range slots {
			for _, tool := range []string{"Read", "Edit"} {
				if r == pathRule(tool, s.Path) {
					return true
				}
			}
		}
		return false
	}
	var written []string
	for _, s := range slots {
		allow := append(append([]string{pathRule("Read", s.Path), pathRule("Edit", s.Path)}, gitAllow...), mcpAllow...)
		deny := slices.Clone(gitDeny)
		for _, o := range slots {
			if o.ID != s.ID {
				deny = append(deny, pathRule("Read", o.Path))
			}
		}
		for _, repo := range slotRepos(s.Path) {
			file := filepath.Join(repo, PermissionsFile)
			if err := mergePermissions(file, allow, deny, crewRule); err != nil {
				return written, err
			}
			gitExclude(repo, PermissionsFile)
			written = append(written, file)
		}
	}
	return written, nil
}

// mergePermissions replaces the crew rules in file's permissions.allow/deny, keeping the rest of
// the file as it is.
func mergePermissions(file string, allow, deny []string, crewRule func(string) bool) error {
	settings := map[string]any{}
	if raw, err := os.ReadFile(file); err == nil {
		if err := json.Unmarshal(raw, &settings); err != nil {
			return err // not ours to fix; leave it alone
		}
	}
	perms, _ := settings["permissions"].(map[string]any)
	if perms == nil {
		perms = map[string]any{}
	}
	merge := func(key string, add []string) {
		var keep []string
		if list, ok := perms[key].([]any); ok {
			for _, v := range list {
				if r, ok := v.(string); ok && !crewRule(r) {
					keep = append(keep, r)
				}
			}
		}
		for _, r := range add {
			if !slices.Contains(keep, r) {
				keep = append(keep, r)
			}
		}
		if len(keep) == 0 {
			delete(perms, key)
		} else {
			perms[key] = keep
		}
	}
	merge("allow", allow)
	merge("deny", deny)
	settings["permissions"] = perms
	wt, _ := settings["worktree"].(map[string]any)
	if wt == nil {
		wt = map[string]any{}
	}
	wt["bgIsolation"] = "none" // edit the slot's checkout directly, no worktree
	settings["worktree"] = wt
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	return os.WriteFile(file, append(out, '\n'), 0o644)
}

// gitExclude keeps path out of the repo's commits via its local .git/info/exclude (not .gitignore,
// so the repo itself doesn't change).
func gitExclude(repo, path string) {
	file := filepath.Join(repo, ".git", "info", "exclude")
	raw, _ := os.ReadFile(file)
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == path {
			return
		}
	}
	os.MkdirAll(filepath.Dir(file), 0o755)
	f, err := os.OpenFile(file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	if len(raw) > 0 && !strings.HasSuffix(string(raw), "\n") {
		f.WriteString("\n")
	}
	f.WriteString("# crew: per-slot Claude permissions\n" + path + "\n")
}
