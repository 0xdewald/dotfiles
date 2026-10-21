#!/usr/bin/env bash
# Sets up the Neovim + Go dev environment from this dotfiles repo.
#
#   ./install.sh               install everything; asks before replacing any existing config
#   ./install.sh --reinstall   same, but also redo config that's already in place (asks each time)
#   ./install.sh hooks         only register the Claude Code hooks (add --reinstall to redo them)
#   ./install.sh shell         only the zsh setup (completion menus, suggestions, fzf keys)
#
# Safe to re-run: installs are idempotent and anything replaced is backed up first.
set -euo pipefail

DOTFILES="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
NVIM_TARGET="$HOME/.config/nvim"
REINSTALL=0
ONLY_HOOKS=0
ONLY_SHELL=0
FZF_TAB_COMMIT=24105b15714bfec37989ed5c5b6e60f572253019

for arg in "$@"; do
  case "$arg" in
    hook | hooks) ONLY_HOOKS=1 ;;
    shell) ONLY_SHELL=1 ;;
    --reinstall | --force) REINSTALL=1 ;;
    -h | --help) sed -n '2,9p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "Unknown argument: $arg (see ./install.sh --help)" >&2; exit 1 ;;
  esac
done

say() { printf '\n\033[1;35m==> %s\033[0m\n' "$*"; }

# ask "question": true on y/Y. Without a terminal to ask on, says so and answers no.
ask() {
  if [[ ! -t 0 ]]; then
    echo "Not running interactively, skipping. Re-run ./install.sh in a terminal to do this."
    return 1
  fi
  local answer
  read -r -p "$1 [y/N] " answer
  [[ "$answer" =~ ^[Yy]$ ]]
}

backup() {
  local b="$1.bak-$(date +%Y%m%d%H%M%S)"
  cp -R "$1" "$b"
  echo "Backed up to $b"
}

# Claude Code hooks from bin/, as "<event>|<command>"
HOOKS=(
  "UserPromptSubmit|~/.local/bin/claude-baseline" # snapshot before each prompt, for Space rv
  "SessionStart|~/.local/bin/claude-session"      # tells Neovim which session its Claude is on
  "UserPromptSubmit|~/.local/bin/crew-hook"       # crew board: agent is working
  "Stop|~/.local/bin/crew-hook"                   # crew board: agent is done (notifies you)
  "Notification|~/.local/bin/crew-hook"           # crew board: agent needs input
)

# Register the hooks in ~/.claude/settings.json. Shows the change as a diff and only writes it
# after you approve. With --reinstall, our existing entries are removed and added again.
install_claude_hooks() {
  local settings="$HOME/.claude/settings.json"
  mkdir -p "$HOME/.local/bin" "$HOME/.claude"
  ln -sf "$DOTFILES/bin/claude-baseline" "$HOME/.local/bin/claude-baseline"
  ln -sf "$DOTFILES/bin/claude-session" "$HOME/.local/bin/claude-session"
  ln -sf "$DOTFILES/bin/crew-hook" "$HOME/.local/bin/crew-hook"

  local tmp; tmp="$(mktemp -d)"
  mkdir -p "$tmp/a" "$tmp/b"
  if [[ -f "$settings" ]]; then cp "$settings" "$tmp/a/settings.json"; else echo '{}' > "$tmp/a/settings.json"; fi

  if ! jq -e . "$tmp/a/settings.json" >/dev/null 2>&1; then
    echo "$settings is not valid JSON, leaving it alone. Add the hooks by hand (see README)." >&2
    rm -rf "$tmp"; return 0
  fi

  local spec
  spec="$(printf '%s\n' "${HOOKS[@]}" | jq -R 'split("|") | {event: .[0], cmd: .[1]}' | jq -s .)"
  jq --argjson spec "$spec" --argjson re "$REINSTALL" '
    [$spec[].cmd] as $ours
    | (if $re == 1 and .hooks then
        .hooks |= (with_entries(.value |= (map(.hooks |= map(select(.command as $c | $ours | index($c) | not)))
                                           | map(select(.hooks | length > 0))))
                   | with_entries(select(.value | length > 0)))
      else . end)
    | reduce $spec[] as $h (.;
        if any(.hooks[$h.event][]?.hooks[]?; .command == $h.cmd) then .
        else .hooks[$h.event] += [{"hooks": [{"type": "command", "command": $h.cmd}]}] end)
    | if .hooks == {} then del(.hooks) else . end
  ' "$tmp/a/settings.json" > "$tmp/b/settings.json"

  if [[ "$(jq -c . "$tmp/a/settings.json")" == "$(jq -c . "$tmp/b/settings.json")" ]]; then
    echo "Hooks already registered."
    rm -rf "$tmp"; return 0
  fi

  echo "Proposed change to $settings:"
  echo
  (cd "$tmp" && git --no-pager diff --no-index --no-prefix --color=always a/settings.json b/settings.json) || true
  echo
  if ask "Apply this change?"; then
    [[ -f "$settings" ]] && backup "$settings"
    cp "$tmp/b/settings.json" "$settings"
    echo "Hooks registered. They apply to new Claude Code sessions."
  else
    echo "Skipped. Run ./install.sh hooks to do it later."
  fi
  rm -rf "$tmp"
}

# zsh: plugins, plus one line in ~/.zshrc that loads zsh/init.zsh (shown as a diff, asks first).
# Secrets and machine-specific settings stay in ~/.zshrc; zsh/init.zsh is what's shared.
install_shell() {
  brew install fzf zsh-autosuggestions zsh-syntax-highlighting zoxide
  local fzf_tab="$HOME/.local/share/zsh/fzf-tab"
  if [[ ! -d "$fzf_tab/.git" ]]; then
    mkdir -p "$(dirname "$fzf_tab")"
    git clone -q https://github.com/Aloxaf/fzf-tab "$fzf_tab"
  fi
  git -C "$fzf_tab" fetch -q origin 2>/dev/null || true
  git -C "$fzf_tab" checkout -q "$FZF_TAB_COMMIT"

  local zshrc="$HOME/.zshrc" line="source \"$DOTFILES/zsh/init.zsh\""
  if grep -qF "$DOTFILES/zsh/init.zsh" "$zshrc" 2>/dev/null; then
    echo "~/.zshrc already loads zsh/init.zsh."
    return 0
  fi
  local tmp; tmp="$(mktemp -d)"
  mkdir -p "$tmp/a" "$tmp/b"
  if [[ -f "$zshrc" ]]; then cp "$zshrc" "$tmp/a/.zshrc"; else : > "$tmp/a/.zshrc"; fi
  { cat "$tmp/a/.zshrc"; printf '\n# Shell setup from the dotfiles repo (completion menus, suggestions, fzf keys)\n%s\n' "$line"; } > "$tmp/b/.zshrc"
  echo "Proposed change to $zshrc:"
  echo
  (cd "$tmp" && git --no-pager diff --no-index --no-prefix --color=always a/.zshrc b/.zshrc) || true
  echo
  if ask "Apply this change?"; then
    [[ -f "$zshrc" ]] && backup "$zshrc"
    cp "$tmp/b/.zshrc" "$zshrc"
    echo "Done. Open a new terminal tab to use it."
  else
    echo "Skipped. Run ./install.sh shell to do it later."
  fi
  rm -rf "$tmp"
}

link_nvim_config() {
  mkdir -p "$HOME/.config"
  if [[ -L "$NVIM_TARGET" && "$(readlink "$NVIM_TARGET")" == "$DOTFILES/nvim" ]]; then
    if [[ $REINSTALL == 0 ]]; then echo "Already linked."; return 0; fi
    ask "Recreate the link $NVIM_TARGET -> $DOTFILES/nvim?" || return 0
    rm "$NVIM_TARGET"
  elif [[ -L "$NVIM_TARGET" ]]; then
    ask "$NVIM_TARGET links to $(readlink "$NVIM_TARGET"). Point it at $DOTFILES/nvim instead?" || return 0
    rm "$NVIM_TARGET"
  elif [[ -e "$NVIM_TARGET" ]]; then
    ask "$NVIM_TARGET already exists. Back it up and replace it with a link to $DOTFILES/nvim?" || return 0
    local b="$NVIM_TARGET.bak-$(date +%Y%m%d%H%M%S)"
    mv "$NVIM_TARGET" "$b"
    echo "Existing config moved to $b"
  fi
  ln -s "$DOTFILES/nvim" "$NVIM_TARGET"
  echo "Linked."
}

if [[ $ONLY_HOOKS == 1 ]]; then
  install_claude_hooks
  exit 0
fi
if [[ $ONLY_SHELL == 1 ]]; then
  install_shell
  exit 0
fi

if [[ "$(uname)" != "Darwin" ]]; then
  echo "This script targets macOS + Homebrew. On Linux, install the same packages with your package manager." >&2
  exit 1
fi

if ! command -v brew >/dev/null; then
  say "Installing Homebrew"
  /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
  eval "$(/opt/homebrew/bin/brew shellenv)"
fi

say "Installing CLI tools (neovim, go, ripgrep, fd, lazygit, tree-sitter, golangci-lint, jq)"
brew install neovim go git ripgrep fd lazygit tree-sitter-cli golangci-lint jq

say "Installing Go tools into \$(go env GOPATH)/bin"
go install golang.org/x/tools/gopls@latest
go install mvdan.cc/gofumpt@latest
go install golang.org/x/tools/cmd/goimports@latest
go install github.com/go-delve/delve/cmd/dlv@latest
go install gotest.tools/gotestsum@latest

say "Claude Code (runs in Neovim's Claude split)"
if command -v claude >/dev/null || [[ -x "$HOME/.local/bin/claude" ]]; then
  echo "Already installed: $("$(command -v claude || echo "$HOME/.local/bin/claude")" --version 2>/dev/null)"
elif ask "Claude Code isn't installed. Install it with the official installer (curl -fsSL https://claude.ai/install.sh | bash)?"; then
  curl -fsSL https://claude.ai/install.sh | bash
else
  echo "Skipped. Install it later from https://claude.com/claude-code, then run: claude"
fi

say "Linking $NVIM_TARGET -> $DOTFILES/nvim"
link_nvim_config

say "Linking nvp (workspace launcher) into ~/.local/bin"
mkdir -p "$HOME/.local/bin"
ln -sf "$DOTFILES/bin/nvp" "$HOME/.local/bin/nvp"

say "Building crew (agent board) into ~/.local/bin"
(cd "$DOTFILES/crew" && go build -o "$HOME/.local/bin/crew" ./cmd/crew) && "$HOME/.local/bin/crew" init

say "Registering Claude Code hooks (review baseline for Space rv, session tracking for Space al)"
install_claude_hooks

say "Setting up zsh (completion menus, suggestions, fzf keys)"
install_shell

say "Adding GOPATH/bin to PATH in ~/.zshrc"
if ! grep -q 'go/bin' "$HOME/.zshrc" 2>/dev/null; then
  printf '\n# Go tools (gopls, dlv, gofumpt, ...)\nexport PATH="$HOME/go/bin:$PATH"\n' >> "$HOME/.zshrc"
fi

if [[ $REINSTALL == 1 ]]; then
  say "Reinstalling Neovim plugins"
  if ask "Delete the installed plugins in ~/.local/share/nvim/lazy and install them again from lazy-lock.json?"; then
    rm -rf "$HOME/.local/share/nvim/lazy"
  fi
fi

say "Installing Neovim plugins (pinned by lazy-lock.json)"
nvim --headless "+Lazy! restore" +qa

say "Compiling treesitter parsers"
nvim --headless -c "lua require('nvim-treesitter').install({
  'go','gomod','gosum','gowork','gotmpl','lua','vim','vimdoc','query','regex',
  'markdown','markdown_inline','json','yaml','toml','bash','sql','proto','dockerfile','make',
}):wait(600000)" +qa

say "Done. Open a new shell and run: nvim"
echo "Icons need a Nerd Font. Ghostty bundles one; for other terminals: brew install --cask font-jetbrains-mono-nerd-font"
