# dotfiles

My Neovim setup for Go development (Neovim 0.12+, macOS, Ghostty).

New here? Start with **[GETTING_STARTED.md](GETTING_STARTED.md)**: install, moving around, working with Claude, and reviewing its changes step by step.

```
dotfiles/
├── install.sh              # one-shot setup script
├── README.md
├── GETTING_STARTED.md      # step-by-step walkthrough
├── zsh/init.zsh            # shell setup, loaded from ~/.zshrc (completion menus, suggestions, fzf keys)
├── bin/
│   ├── nvp                 # workspace launcher, symlinked to ~/.local/bin/nvp
│   ├── claude-baseline     # Claude Code hook: snapshot before each prompt (symlinked too)
│   ├── claude-session      # Claude Code hook: tells Neovim which session its Claude is on
│   └── crew-hook           # Claude Code hook: reports crew agents' activity to the board
├── crew/                   # crew: the agent board (Go; CLI, HTTP API, MCP server); TRAINING.md walks through it
└── nvim/                   # symlinked to ~/.config/nvim
    ├── init.lua            # options, keymaps, statuscolumn, lazy.nvim bootstrap
    ├── lazy-lock.json      # pinned plugin versions
    ├── workspaces/         # your multi-repo workspaces (git-ignored; README.md has an example)
    └── lua/
        ├── workspace.lua   # :Workspace, tab-per-repo sessions, tabline
        ├── review.lua      # review comments on agent changes, sent to Claude
        ├── crew.lua        # crew client: agent dashboard, open an agent, route review comments
        ├── agent.lua       # Claude per folder/workspace: auto-start, resume, sessions; keeps its window in place
        └── plugins/
            ├── ui.lua      # theme, picker, statusline, git, motions
            ├── ai.lua      # claudecode.nvim (Claude Code in Neovim), diffview.nvim
            ├── go.lua      # treesitter, LSP, completion, format, lint, debug, test
            └── session.lua # persistence.nvim (per-directory sessions)
```

## Install

```sh
git clone <this-repo> ~/dotfiles   # or copy the folder over
~/dotfiles/install.sh
```

The script installs everything below, links `~/.config/nvim` to `~/dotfiles/nvim`, registers two Claude Code hooks, puts `~/go/bin` on your PATH, restores plugins at the versions in `lazy-lock.json`, and compiles the treesitter parsers. Before replacing anything that already exists (an old `~/.config/nvim`, your `~/.claude/settings.json`) it shows what will change and asks, and it backs up what it replaces. You can run it again safely.

```sh
./install.sh --reinstall   # also redo config that's already in place: relink, re-register hooks,
                           # optionally reinstall plugins. Still asks before each change.
./install.sh hooks         # only the Claude Code hooks (add --reinstall to redo them)
./install.sh --help
```

Because `~/.config/nvim` is a symlink, any change you make to the config lands in this repo. Commit `lazy-lock.json` after `:Lazy update` so other machines get the same plugin versions.

## What's installed

**CLI tools (Homebrew):** neovim, go, ripgrep, fd, lazygit, tree-sitter-cli, golangci-lint, jq

**Claude Code:** installed with the official installer if it's missing (asks first).

**Shell (Homebrew):** fzf, zsh-autosuggestions, zsh-syntax-highlighting, zoxide; fzf-tab (cloned to `~/.local/share/zsh/fzf-tab`, pinned in `install.sh`)

**Go tools (`go install`):** gopls, gofumpt, goimports, dlv (delve), gotestsum

**Plugins (lazy.nvim):**

| Area | Plugins |
|---|---|
| Look | catppuccin (mocha), lualine, noice + nui, mini.icons, which-key (helix preset) |
| Picker / files / git | snacks.nvim (dashboard, picker, explorer, lazygit, notifier, indent, smooth scroll, terminal), gitsigns |
| Syntax | nvim-treesitter (`main` branch) |
| LSP | nvim-lspconfig + native `vim.lsp.config("gopls")` with gofumpt, staticcheck, inlay hints, codelenses |
| Completion | blink.cmp (super-tab preset, ghost text, signature help) + friendly-snippets |
| Format / lint | conform.nvim (goimports → gofumpt on save), nvim-lint (golangci-lint) |
| Debug | nvim-dap, nvim-dap-go, nvim-dap-view, nvim-dap-virtual-text |
| Tests | neotest + neotest-golang (gotestsum runner) |
| Editing | flash.nvim, mini.pairs, mini.surround (`gs` prefix), mini.ai, trouble.nvim |
| AI / review | claudecode.nvim (Claude Code IDE integration, snacks terminal), diffview.nvim |

**Custom bits in `init.lua`:**
- A two-number line column (absolute │ relative) with its own colours, using `statuscolumn`. The colours are re-applied on every `ColorScheme` event so the theme doesn't overwrite them. Git and diagnostic signs sit to the left (`%s`).
- System clipboard, persistent undo, rounded borders on floating windows, highlight on yank, and `~/go/bin` added to PATH inside Neovim.

## Crew (v0.1.1, in progress)

A local board for running several Claude agents side by side, each in its own checkout ("slot") on its own branch. The board (`crew`, a Go binary in `crew/`) keeps track of work items, agents, slots, branches, messages and comments. Hooks report agent activity to it. Neovim is one client; the CLI, a local HTTP API and an MCP server are the others, so a custom app can replace the Neovim side later. **New to crew? Start with [`crew/TRAINING.md`](crew/TRAINING.md)**, a hands-on walkthrough with copy-paste steps. All commands: [`crew/README.md`](crew/README.md).

What works so far (milestone 1):
- A slot is the agent's own checkout, separate from yours. Create one with `crew slot add slot-one ~/agents/slot-one/core --clone <repo url>` (or clone it yourself and leave out `--clone`), then `crew slot trust slot-one` once so Claude may work there.
- `crew ticket add "read jira ticket VD-123 …"`, `crew item new --ticket 1`, `crew item move 1 queued`, then `crew agent start 1` starts a background Claude on `agent/<ticket>` in a free slot, with its own ports and process-name prefix.
- `Space mm` opens the crew dashboard: agents with status, stage, branch and last message, refreshed every few seconds. Enter opens the agent in a new tab, with its session in the Claude split. `Ctrl-s` sends it a message, `Ctrl-x` stops it.
- Review comments (`Space rc`, `Space rs`) on an agent's branch go to the board, which hands them to that agent: straight into the Claude split if it's open in Neovim, otherwise by resuming it in the background.
- When an agent finishes a turn you get a macOS notification ("crew: Fuzzy Muffins is done").
- `Space rN` reviews a range of commits.

Not yet: the tech lead, plan reviews, the reviewer agent and the director chat (milestones 2 to 4).

## Shell (zsh)

`zsh/init.zsh` is loaded from `~/.zshrc` (`install.sh` adds that one line after showing the diff; `./install.sh shell` runs just that step). Secrets and machine-specific settings stay in `~/.zshrc`.

- **Tab opens a searchable menu** (fzf-tab): type to filter, `<` / `>` switch groups. `git checkout <Tab>` lists branches first, with each one's latest commit and a log preview; modified files only come up when nothing else matches, because checking out a file throws away its changes. Works for git, make targets, gh, brew, and `pm2 restart/logs/... <Tab>` lists your processes.
- **Grey suggestions** while you type (zsh-autosuggestions), predicted from what you usually run after the previous command, then from history. `→` accepts.
- **fzf keys:** `Ctrl-R` search history, `Ctrl-T` pick files, `Option-C` jump to a folder. A grey reminder sits on the right of the prompt.
- **`z <part of a name>`** jumps to a folder you visit often (zoxide).
- Commands turn red while typing if they don't exist (zsh-syntax-highlighting).
- History keeps 50,000 commands, shared between open terminals.

For `Option` keys to work as `Alt` in Ghostty, set `macos-option-as-alt = true` (`Option-C` also works without it).

## Workspaces

Open several repos at once, one tab per repo: `nvp <name>`.

- A workspace is `nvim/workspaces/<name>.lua` returning a list of repo paths, for example:

  ```lua
  -- nvim/workspaces/shop.lua → `nvp shop`
  return {
    "~/code/shop-api",
    "~/code/shop-web",
  }
  ```

  These files list your own repos, so git ignores them; see [`nvim/workspaces/README.md`](nvim/workspaces/README.md). Run `nvp` with no name to list yours.
- Each tab has its own working directory (`:tcd`), so pickers, grep, lazygit and git signs follow the repo of the current tab. The tabline shows the repo names and the workspace name.
- The first open builds the tabs from the file. On exit, the layout (open files, splits, tabs) is saved to `~/.local/state/nvim/workspaces/` and restored next time. After editing the workspace file, run `:WorkspaceReset` to rebuild the tabs from it.
- Inside Neovim: `<leader>w` switches workspace, `]t` / `[t` move between repo tabs, `:Workspace <name>` opens one.
- For plain `nvim` in a single folder, persistence.nvim saves a session per directory: `<leader>qs` restores it, `<leader>ql` restores the last one, `<leader>fp` picks a recent project.

## Claude in Neovim

Each folder or workspace has its own Claude session, which lives alongside Neovim:

- **Starts with Neovim** when you open a folder (`nvim`, `nvim .`, `nvim ~/some/repo`) or a workspace (`nvp x`). Opening a single file (`nvim notes.txt`, git commit messages) or starting in your home folder doesn't start Claude; `Space ac` does when you want it.
- **Resumes next time.** Claude exits when Neovim does, but the conversation is kept, and the next Neovim in that folder or workspace picks it up again. This keeps working after `/clear` inside Claude, thanks to the `SessionStart` hook (`bin/claude-session`).
- `Space an` starts a fresh session. The old one stays available.
- `Space al` lists this folder's sessions, newest first, with a preview of the conversation. Enter switches to one, `Ctrl-n` starts a new one, `Ctrl-x` deletes one (asks first).
- The Claude window stays where it is: if a file would open in it (for example from the explorer when no editor window is open), Claude stays and the file opens beside it. Its width is kept at 35%.
- Set `vim.g.agent_autostart = false` to turn auto-start off.

In a workspace there's one Claude, named after the workspace, running in the first tab's repo.

## Reviewing agent changes

Run Claude Code inside Neovim, review what it changed, leave line comments, and send them back.

1. Claude runs in a right-hand split (see above; `Space ac` shows or hides it). claudecode.nvim runs the same WebSocket IDE server VS Code uses, so Claude sees your selection, can open files, and proposes edits as diffs (`Space aa` / `Space ad` accept / deny). `Space as` sends a visual selection, `Space ab` adds the current file.
2. When it's done, `Space rv` diffs **the last agent turn** (working tree vs the snapshot taken when you sent the prompt) and `Space rV` diffs **all changes** vs HEAD. `Space rq` closes the diff.
3. On any line (or visual range), `Space rc` adds a comment. It shows as a virtual line above the code, also in diffview's right-hand side. `Space rd` deletes and `Space rr` resolves the comment under the cursor; `Space rl` lists them (Enter jumps, `Ctrl-x` deletes, `Ctrl-r` resolves). Comments belong to a review: `Space rn` closes the current one (kept in history), `Space rR` lists reviews to bring one back or delete it, `Space rX` deletes the current one, `Space rt` hides/shows comments.
4. `Space rs` sends all open comments to Claude as one numbered prompt (`path:lines`, the code, your comment). With more than 5 it writes them to `.git/review.md` and sends a short message pointing at that file. Sent comments are dimmed and marked `(sent)`; if everything was already sent, `Space rs` offers to send it again, and `Space rS` always sends every unresolved comment.

Comments are stored per repo in `.git/review-comments.json`, so they survive restarts and each workspace tab has its own. If the code moves, a comment re-attaches to its original lines by searching for them nearby; if they're gone it stays on the original line number.

**Hooks.** "Last agent turn" needs `bin/claude-baseline`, and session resume needs `bin/claude-session`, both registered as Claude Code hooks in `~/.claude/settings.json`. `install.sh` does this: it shows the change as a diff and only writes it if you answer `y` (the old file is backed up to `settings.json.bak-<timestamp>`). To run just that step: `./install.sh hooks`. The resulting entries:

```json
{
  "hooks": {
    "UserPromptSubmit": [
      { "hooks": [{ "type": "command", "command": "~/.local/bin/claude-baseline" }] }
    ],
    "SessionStart": [
      { "hooks": [{ "type": "command", "command": "~/.local/bin/claude-session" }] }
    ]
  }
}
```

`claude-session` only acts for a Claude that Neovim started; for other Claude sessions it does nothing.

On every prompt it writes `git stash create` (or HEAD when clean) to `.git/claude-baseline` and the list of untracked files to `.git/claude-baseline-untracked`. Neither touches your index or stash. `git stash create` ignores untracked files and diffview doesn't list untracked files when diffing against a commit, so `Space rv` marks files that appeared since the baseline as intent-to-add (`git add -N`) to show them as added. Edits Claude makes to files that were *already* untracked don't show in the turn diff.

**Multi-repo workspaces.** There is one Claude per Neovim (one server, one terminal). It starts in the cwd of the tab you first open it from; toggling it from another tab shows the same session and tells you where it's running. Comments and diffs always use the repo of the current buffer (or tab), and when that isn't Claude's directory the prompt uses absolute paths. The baseline hook only snapshots the repo Claude runs in.

**Per-edit diffs.** claudecode.nvim has no option to skip its proposed-edit diffs, so they're left on. To review only at the end, switch Claude to accept-edits mode (`Shift-Tab` in Claude, or start it with `--permission-mode acceptEdits`) and use `Space rv`.

## Keymaps

Leader is **Space**. Press it and wait to see everything in which-key.

| Keys | Action |
|---|---|
| `Space Space` | Smart file finder |
| `Space ff` / `fg` / `fw` / `fb` / `fr` | Files / grep / grep word / buffers / recent |
| `Space fs` / `fS` / `fd` | Document symbols / workspace symbols / diagnostics |
| `Space e` | File explorer |
| `Space gg` / `gl` / `gs` / `gB` | Lazygit / git log / git status / open in GitHub |
| `]h` `[h`, `Space gp` / `gr` / `gb` | Next/prev hunk, preview / reset hunk, blame line |
| `gd` `gr` `gI` `gy` `K` | Definition, references, implementation, type definition, hover |
| `]]` `[[` | Next/prev reference of the word under the cursor |
| `Space ca` / `cr` / `cf` / `cl` / `ch` | Code action / rename / format / run codelens / toggle inlay hints |
| `Space tt` / `tf` / `tA` / `tl` / `ts` / `to` / `tw` | Test: nearest / file / all / last / summary / output / watch |
| `Space db` / `dc` / `do` / `di` / `dO` / `dt` / `dq` / `dv` | Debug: breakpoint / continue / over / into / out / nearest test / stop / view |
| `Space xx` / `xb` / `xs` | Trouble: diagnostics / buffer diagnostics / symbols |
| `s` / `S` | Flash jump / flash treesitter select |
| `gsa` `gsd` `gsr` | Surround: add / delete / replace |
| `Ctrl-/` | Toggle terminal |
| `Ctrl-h/j/k/l` | Move between splits (also from inside terminals) |
| `Ctrl-s` | Save |
| `Space qq` | Quit Neovim, Claude and terminals, back to the shell (asks about unsaved files) |
| `Space n` / `Space bd` | Notification history / delete buffer |
| `Space ac` / `an` / `al` | Claude: show-hide (starts or resumes this folder's session) / new session / list sessions |
| `Space af` / `ar` / `aC` / `am` | Claude: focus / Claude's own resume picker / continue last conversation / pick model |
| `Space as` (visual) / `ab` / `aa` / `ad` | Claude: send selection / add buffer / accept diff / deny diff |
| `Space rc` / `rl` / `rd` / `rr` / `rs` | Review: comment (line or selection) / list / delete / resolve / send to Claude |
| `Space rN` | Review: diff a range of commits (last n, or since a commit/branch) |
| `Space mm` / `ms` | Crew: dashboard of agents / board status |
| `Space rS` / `rn` / `rR` / `rX` / `rt` | Review: send all unresolved again / close review / list reviews / delete review / hide-show comments |
| `Space rv` / `rV` / `rq` | Review: diff last agent turn / diff all vs HEAD / close diff |

## Rebuild with an AI instead

If you'd rather have an AI coding agent rebuild this on a new machine (for example where the script doesn't apply, such as Linux), give it this prompt along with the `nvim/` folder:

> Set up my Neovim (0.12+) Go development environment from the `nvim/` folder in this dotfiles repo.
> 1. Install with the system package manager: neovim, go, git, ripgrep, fd, lazygit, tree-sitter CLI (0.25+), golangci-lint, and a C compiler.
> 2. Run `go install` for gopls, mvdan.cc/gofumpt, golang.org/x/tools/cmd/goimports, github.com/go-delve/delve/cmd/dlv, and gotest.tools/gotestsum. Add `$(go env GOPATH)/bin` to the shell PATH.
> 3. Back up any existing `~/.config/nvim`, then symlink `~/.config/nvim` to `<dotfiles>/nvim`.
> 4. Run `nvim --headless "+Lazy! restore" +qa`, then install the treesitter parsers listed in `nvim/lua/plugins/go.lua` with `require('nvim-treesitter').install({...}):wait(600000)`.
> 5. Check it works: open a `.go` file in a small Go module headlessly and confirm gopls attaches, treesitter highlighting is active, the colorscheme is `catppuccin-latte`, and saving reformats the file with gofumpt.
> 6. Make sure the terminal uses a Nerd Font so the icons show.
> Don't change the config files. If a plugin API has changed, report it rather than quietly rewriting the config.

## Troubleshooting

- `:checkhealth` covers most problems. Also try `:Lazy`, `:LspInfo` (or `:checkhealth vim.lsp`) and `:ConformInfo`.
- Icons show as boxes → the terminal font isn't a Nerd Font.
- gopls or dlv "not found" → `~/go/bin` isn't on PATH. Run `go env GOPATH`.
- Parser errors → check that `tree-sitter --version` is 0.25 or newer, then run `:TSUpdate`.
