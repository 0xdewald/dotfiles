#!/usr/bin/env bash
# Sets up the Neovim + Go dev environment from this dotfiles repo.
# Safe to re-run: installs are idempotent and any existing config is backed up.
set -euo pipefail

DOTFILES="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
NVIM_TARGET="$HOME/.config/nvim"

say() { printf '\n\033[1;35m==> %s\033[0m\n' "$*"; }

if [[ "$(uname)" != "Darwin" ]]; then
  echo "This script targets macOS + Homebrew. On Linux, install the same packages with your package manager." >&2
  exit 1
fi

if ! command -v brew >/dev/null; then
  say "Installing Homebrew"
  /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
  eval "$(/opt/homebrew/bin/brew shellenv)"
fi

say "Installing CLI tools (neovim, go, ripgrep, fd, lazygit, tree-sitter, golangci-lint)"
brew install neovim go git ripgrep fd lazygit tree-sitter-cli golangci-lint

say "Installing Go tools into \$(go env GOPATH)/bin"
go install golang.org/x/tools/gopls@latest
go install mvdan.cc/gofumpt@latest
go install golang.org/x/tools/cmd/goimports@latest
go install github.com/go-delve/delve/cmd/dlv@latest
go install gotest.tools/gotestsum@latest

say "Linking $NVIM_TARGET -> $DOTFILES/nvim"
mkdir -p "$HOME/.config"
if [[ -L "$NVIM_TARGET" ]]; then
  rm "$NVIM_TARGET"
elif [[ -e "$NVIM_TARGET" ]]; then
  backup="$NVIM_TARGET.bak-$(date +%Y%m%d%H%M%S)"
  echo "Existing config moved to $backup"
  mv "$NVIM_TARGET" "$backup"
fi
ln -s "$DOTFILES/nvim" "$NVIM_TARGET"

say "Linking nvp (workspace launcher) into ~/.local/bin"
mkdir -p "$HOME/.local/bin"
ln -sf "$DOTFILES/bin/nvp" "$HOME/.local/bin/nvp"

say "Adding GOPATH/bin to PATH in ~/.zshrc"
if ! grep -q 'go/bin' "$HOME/.zshrc" 2>/dev/null; then
  printf '\n# Go tools (gopls, dlv, gofumpt, ...)\nexport PATH="$HOME/go/bin:$PATH"\n' >> "$HOME/.zshrc"
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
