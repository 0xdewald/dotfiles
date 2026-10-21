# Getting started

A walkthrough from a fresh install to reviewing Claude's changes and sending comments back, all inside Neovim. For the full keymap list and plugin details, see the [README](README.md).

Leader is **Space**. When this guide says `Space r c`, press Space, then `r`, then `c`. If you pause after Space, a menu (which-key) shows what's available.

## 1. Install

```sh
git clone https://github.com/0xdewald/dotfiles ~/dotfiles
cd ~/dotfiles
./install.sh
```

The script installs Neovim, Go and the tools, links `~/.config/nvim` to this repo, and installs the plugins. Near the end it shows the change it wants to make to `~/.claude/settings.json` (two hooks: one runs before each prompt, one when a Claude session starts) and asks before writing it. Answer `y`. To run just that step later: `./install.sh hooks`.

Already installed and want to redo the setup, for example after pulling changes? `./install.sh --reinstall` redoes the config that's already in place (the link, the hooks, and optionally the plugins), and still asks before each change.

It also offers to add one line to `~/.zshrc` that loads the shell setup: Tab menus you can search (try `git checkout <Tab>`), grey suggestions as you type (`→` accepts), and `Ctrl-R` / `Ctrl-T` / `Option-C` for history, files and folders.

Open a new terminal afterwards so the updated `PATH` and shell setup take effect. Your terminal needs a Nerd Font for the icons (Ghostty has one built in).

## 2. Find your way around the screen

A typical layout once Claude is open:

```
┌────────────┬──────────────────────────┬───────────────┐
│ Explorer   │ Editor                   │ Claude        │
│ (Space e)  │                          │ (Space a c)   │
├────────────┴──────────────────────────┴───────────────┤
│ Shell terminal (Ctrl-/)                               │
└───────────────────────────────────────────────────────┘
```

**Move between windows with `Ctrl-h` `Ctrl-j` `Ctrl-k` `Ctrl-l`** (left, down, up, right). This works from the editor and from inside the Claude and shell terminals.

**Terminals have their own mode.** While you're in the Claude or shell window, every key goes to that program. That's how you type to Claude. The status bar says `TERMINAL` when this is the case.
- To leave a terminal: `Ctrl-h/j/k/l` to move straight to another window, or `Ctrl-\` then `Ctrl-n` to stay in the window and scroll it.
- To type into a terminal again: move into it and press `i`.
- Don't use `Esc` to leave the Claude window. In Claude, `Esc` interrupts what it's doing.

The Claude window stays where it is. If a file would open in it (for example from the explorer when no editor window is open), Claude stays and the file opens in an editor window beside it.

## 3. Open a project

- **One repo:** `cd` into it and run `nvim`.
- **Several repos together:** `nvp <workspace>` opens one tab per repo. Create one by adding a file to `nvim/workspaces/` (see the example in [`nvim/workspaces/README.md`](nvim/workspaces/README.md)); run `nvp` alone to list yours. Move between repo tabs with `]t` and `[t`.

**`Space q q` quits** Neovim, Claude and the terminals in one go and takes you back to the shell that started it. If a file has unsaved changes, it asks whether to save them first.

Your open files and splits are saved when you quit. `Space q s` restores them for a plain folder; workspaces restore by themselves.

## 4. Work with Claude

Press `Space a c` to open Claude on the right, connected to Neovim, so it sees the file you're in and what you've selected. Each folder or workspace has its own conversation: quit Neovim and come back later, press `Space a c`, and Claude carries on where you left off.

- `Space a c` shows or hides Claude, and starts it the first time.
- `Space a n` starts a fresh conversation. The old one is kept.
- `Space a l` lists this folder's conversations with a preview. Enter switches to one.

Then:

1. Move to Claude with `Ctrl-l` and press `i` to type.
2. Optional: press `Shift-Tab` in the Claude window until it shows accept-edits mode. Claude then makes its changes without asking you about each one, and you review them all at the end (step 5). Without it, Claude shows each edit as a diff first: `Space a a` accepts, `Space a d` rejects.
3. Type your request and press Enter.

Other useful keys: select code in visual mode and press `Space a s` to send it to Claude, or press `Space a b` to add the whole current file.

## 5. Review Claude's changes

When Claude finishes a turn, press **`Space r v`**. This opens a side-by-side diff of only what Claude changed in its last turn, including new files. `Space r V` shows everything since your last commit instead, and `Space r q` closes the diff.

**Comments are change requests.** Put the cursor on a line, in a file or on either side of any diff (`Space r v`, `Space r V` or a range of commits with `Space r N`), and press `Space r c`. What you write is a request to change that code. Comments collect until you send them.

| Keys | Action |
|---|---|
| `Space r c` | Comment on the current line. In visual mode (`V`), on the selected lines. Works on the old (left) side of a diff too. |
| `Space r l` | List this repo's comments. Enter jumps to one. `Tab` selects several, then `Ctrl-s` sends them (again), `Ctrl-r` resolves, `Ctrl-o` marks them open again, `Ctrl-x` deletes. |
| `Space r d` / `Space r r` | Delete / resolve the comment under the cursor |
| `Space r t` | Hide or show all comments |

Comments show up next to the code, in the diff and in the file itself. They're saved in the repo's `.git` folder, so they survive restarts and are never committed.

## 6. Send comments back to Claude

Press **`Space r s`**. Whoever owns the code gets one numbered message with each comment's file, lines, the code, and what you wrote: the crew agent working on that branch, or otherwise your Claude in the side panel. A comment made on a commit or on the old side of a diff says so ("in commit a1b2c3d", "before the change"). With more than 5 comments to your own Claude, they're written to `.git/review.md` and Claude is asked to read that file.

If you close a diff, or quit with `Space q q`, while comments haven't been sent, you're reminded. On quit you can send them, quit anyway, or cancel.

Sent comments stay on screen, dimmed and marked "(sent)", until you resolve them. That's on purpose: you check each one against the fix.
- `Space r s` sends only comments that haven't been sent yet. If they've all been sent, it asks whether to send them again.
- `Space r S` sends every comment that isn't resolved, including ones already sent.
- To resend just some: `Space r l`, select them with `Tab`, then `Ctrl-s`.

When the fixes are in, press `Space r v` again. You now see only the fixes, not the earlier work. Resolve comments that are handled (`Space r r`), and add new ones for anything that isn't.

## 7. Try it end to end

A throwaway Go project, so none of your real repos are touched:

```sh
mkdir -p ~/tmp/review-test && cd ~/tmp/review-test
git init -q && go mod init reviewtest
printf 'package main\n\nfunc Add(a, b int) int { return a + b }\n\nfunc main() {}\n' > main.go
git add . && git commit -qm init
nvim main.go
```

| # | Do | You should see |
|---|---|---|
| 1 | `Space a c`, then `Shift-Tab` in Claude until accept-edits mode shows | Claude opens on the right |
| 2 | Ask Claude: *"Add `Divide(a, b int) int` to main.go and a main_test.go with tests for Add and Divide"* | Claude edits `main.go` and creates `main_test.go` |
| 3 | `Ctrl-h` back to the editor, then `Space r v` | A diff with `main.go` modified and `main_test.go` added |
| 4 | On the `Divide` line: `Space r c` → *"panics when b == 0, return an error instead"* | The comment appears next to the line |
| 5 | In `main_test.go`, select a test with `V` and a motion, then `Space r c` → *"make this table-driven"* | A comment on the selected range |
| 6 | `Space r l` | Both comments listed |
| 7 | `Space r s` | Your comments arrive in Claude as one message, and dim in the editor |
| 8 | When Claude is done: `Space r v` | Only the second turn's changes (error return, table-driven test) |
| 9 | `Space r r` on each comment | Each disappears as it's resolved |
| 10 | Quit (`:qa`), reopen `nvim main.go`, `Space r l` | Unresolved comments are still there |

## Troubleshooting

- **Stuck in a terminal, keys don't move you out:** press `Ctrl-\` then `Ctrl-n`, then `Ctrl-h/j/k/l`.
- **`Space r v` shows everything since the last commit, not just Claude's last turn:** the hook isn't running. Check that `cat .git/claude-baseline` prints a commit hash. If not, run `./install.sh hooks` in `~/dotfiles`, then restart Claude with `Space a n` (hooks only apply to Claude sessions started after they're installed).
- **Claude starts a new conversation instead of resuming:** check that `./install.sh hooks` says "Hooks already registered". Without the `SessionStart` hook, resuming still works, but not after `/clear`.
- **Want Claude to start by itself** when you open a folder or workspace: add `vim.g.agent_autostart = true` to `nvim/init.lua`.
- **`Space r s` does nothing:** Claude isn't open. Press `Space a c` first.
- **Comments stay after sending:** that's expected. Resolve them (`Space r r`) once they're handled.
- **Claude doesn't seem connected to Neovim:** run `:ClaudeCodeStatus`.
- **Several repo tabs, one Claude:** there's one Claude per Neovim, and it runs in the repo of the tab you first opened it from. Comments on other repos still work, because they're sent with full paths.
- **New files Claude made show in `git status` as added:** `Space r v` marks them with `git add -N` so the diff can show them. Nothing is committed.

## Next: running several agents (crew)

Once you're comfortable with one Claude in Neovim, [`crew/TRAINING.md`](crew/TRAINING.md) walks you through running several agents side by side, each in its own checkout and branch, with copy-paste steps.
