# dotfiles

My Neovim setup for Go development (Neovim 0.12+, macOS, Ghostty).

```
dotfiles/
├── install.sh              # one-shot setup script
├── README.md
├── bin/nvp                 # workspace launcher, symlinked to ~/.local/bin/nvp
└── nvim/                   # symlinked to ~/.config/nvim
    ├── init.lua            # options, keymaps, statuscolumn, lazy.nvim bootstrap
    ├── lazy-lock.json      # pinned plugin versions
    ├── workspaces/         # one file per multi-repo workspace
    └── lua/
        ├── workspace.lua   # :Workspace, tab-per-repo sessions, tabline
        └── plugins/
            ├── ui.lua      # theme, picker, statusline, git, motions
            ├── go.lua      # treesitter, LSP, completion, format, lint, debug, test
            └── session.lua # persistence.nvim (per-directory sessions)
```

## Install

```sh
git clone <this-repo> ~/dotfiles   # or copy the folder over
~/dotfiles/install.sh
```

The script installs everything below, links `~/.config/nvim` to `~/dotfiles/nvim` (and backs up any existing config), puts `~/go/bin` on your PATH, restores plugins at the versions in `lazy-lock.json`, and compiles the treesitter parsers. You can run it again safely.

Because `~/.config/nvim` is a symlink, any change you make to the config lands in this repo. Commit `lazy-lock.json` after `:Lazy update` so other machines get the same plugin versions.

## What's installed

**CLI tools (Homebrew):** neovim, go, ripgrep, fd, lazygit, tree-sitter-cli, golangci-lint

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

**Custom bits in `init.lua`:**
- A two-number line column (absolute │ relative) with its own colours, using `statuscolumn`. The colours are re-applied on every `ColorScheme` event so the theme doesn't overwrite them. Git and diagnostic signs sit to the left (`%s`).
- System clipboard, persistent undo, rounded borders on floating windows, highlight on yank, and `~/go/bin` added to PATH inside Neovim.

## Workspaces

Open several repos at once, one tab per repo: `nvp coworker`.

- A workspace is `nvim/workspaces/<name>.lua` returning a list of repo paths. Run `nvp` with no name to list them.
- Each tab has its own working directory (`:tcd`), so pickers, grep, lazygit and git signs follow the repo of the current tab. The tabline shows the repo names and the workspace name.
- The first open builds the tabs from the file. On exit, the layout (open files, splits, tabs) is saved to `~/.local/state/nvim/workspaces/` and restored next time. After editing the workspace file, run `:WorkspaceReset` to rebuild the tabs from it.
- Inside Neovim: `<leader>w` switches workspace, `]t` / `[t` move between repo tabs, `:Workspace <name>` opens one.
- For plain `nvim` in a single folder, persistence.nvim saves a session per directory: `<leader>qs` restores it, `<leader>ql` restores the last one, `<leader>fp` picks a recent project.

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
| `Ctrl-h/j/k/l` | Move between splits |
| `Ctrl-s` | Save |
| `Space n` / `Space bd` | Notification history / delete buffer |

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
