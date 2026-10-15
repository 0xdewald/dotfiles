# Shell setup from the dotfiles repo, loaded by ~/.zshrc (install.sh adds the line).
# Keep secrets and machine-specific settings in ~/.zshrc, not here.

# --- PATH -----------------------------------------------------------------------------------
[[ -z $HOMEBREW_PREFIX && -x /opt/homebrew/bin/brew ]] && eval "$(/opt/homebrew/bin/brew shellenv)"
typeset -U path
path=($HOME/.local/bin $HOME/go/bin $path)

# --- History: large, shared between open terminals, used for suggestions ---------------------
HISTFILE=$HOME/.zsh_history
HISTSIZE=50000
SAVEHIST=50000
setopt extended_history share_history hist_ignore_all_dups hist_ignore_space hist_reduce_blanks

# --- Completion -------------------------------------------------------------------------------
# Homebrew's completions go last (brew shellenv puts them first) so zsh's own, richer _git
# wins over brew's bash wrapper
if [[ -n $HOMEBREW_PREFIX ]]; then
  fpath=(${fpath:#$HOMEBREW_PREFIX/share/zsh/site-functions} $HOMEBREW_PREFIX/share/zsh/site-functions)
fi
mkdir -p $HOME/.cache/zsh
autoload -Uz compinit && compinit -d $HOME/.cache/zsh/zcompdump

zstyle ':completion:*' matcher-list 'm:{a-z}={A-Z}' 'r:|[._-]=* r:|=*'  # case-insensitive, fuzzy-ish
zstyle ':completion:*' menu no                                         # fzf-tab draws the menu
zstyle ':completion:*:descriptions' format '[%d]'                      # group headers in fzf-tab
zstyle ':completion:*' list-colors ${(s.:.)LS_COLORS}
zstyle ':completion:*:git-checkout:*' sort false                       # keep recent branches first
# git checkout <Tab>: offer branches/commits, and modified files only when no branch matches what
# you typed. Picking a file there runs `git checkout <file>`, which throws away its changes.
zstyle ':completion:*:*:git-checkout:*' tag-order 'tree-ishs' 'modified-files'

# pm2: subcommands, then your process names for the commands that take one
_pm2() {
  local -a cmds procs
  if (( CURRENT == 2 )); then
    cmds=(start stop restart reload delete list logs describe monit flush save resurrect status)
    _describe 'pm2 command' cmds
  elif [[ ${words[2]} == (stop|restart|reload|delete|logs|describe|flush) ]]; then
    procs=(${(f)"$(pm2 jlist 2>/dev/null | jq -r '.[].name' 2>/dev/null)"} all)
    _describe 'process' procs
  else
    _files
  fi
}
compdef _pm2 pm2

# --- fzf: Ctrl-R history, Ctrl-T files, Alt-C folders ----------------------------------------
if (( $+commands[fzf] )); then
  source <(fzf --zsh)
  bindkey 'ç' fzf-cd-widget  # Option-C when the terminal doesn't send it as Alt-C
fi

# --- fzf-tab: Tab opens a searchable menu, with previews ---------------------------------------
if [[ -r $HOME/.local/share/zsh/fzf-tab/fzf-tab.plugin.zsh ]]; then
  source $HOME/.local/share/zsh/fzf-tab/fzf-tab.plugin.zsh
  zstyle ':fzf-tab:*' switch-group '<' '>'
  zstyle ':fzf-tab:complete:git-(checkout|switch|merge|rebase|log|diff):*' fzf-preview \
    'git log --oneline --graph --color=always -20 $word 2>/dev/null || git diff --color=always -- $word'
  zstyle ':fzf-tab:complete:(cd|z|ls|nvim):*' fzf-preview \
    '[[ -d $realpath ]] && ls -G $realpath || head -50 $realpath'
fi

# --- zoxide: `z <part of a folder name>` jumps to folders you visit often ----------------------
(( $+commands[zoxide] )) && eval "$(zoxide init zsh)"

# --- Suggestions: grey text predicting the rest of the command (→ accepts it) -----------------
# match_prev_cmd: what you usually run after the previous command; then plain history; then Tab
if [[ -r $HOMEBREW_PREFIX/share/zsh-autosuggestions/zsh-autosuggestions.zsh ]]; then
  ZSH_AUTOSUGGEST_STRATEGY=(match_prev_cmd history completion)
  ZSH_AUTOSUGGEST_HIGHLIGHT_STYLE='fg=8'
  source $HOMEBREW_PREFIX/share/zsh-autosuggestions/zsh-autosuggestions.zsh
fi

# --- Key hints in grey on the right of the prompt (hidden on long lines, gone after Enter) -----
setopt transient_rprompt
RPROMPT='%F{8}^R history · ^T files · ⌥C folders · Tab complete%f'

# --- Syntax highlighting: must be loaded last -------------------------------------------------
[[ -r $HOMEBREW_PREFIX/share/zsh-syntax-highlighting/zsh-syntax-highlighting.zsh ]] &&
  source $HOMEBREW_PREFIX/share/zsh-syntax-highlighting/zsh-syntax-highlighting.zsh
