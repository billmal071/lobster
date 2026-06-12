# yadm Dotfiles Migration Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Migrate from symlink-based dotfiles to yadm for cross-platform config management (Linux + macOS).

**Architecture:** yadm manages dotfiles in-place in `$HOME`, using Git under the hood. OS-specific shell configs use yadm alternate files (`##os.Linux` / `##os.Darwin`). A shared shell file holds cross-platform aliases and exports.

**Tech Stack:** yadm, bash, zsh, Git

**Spec:** `docs/superpowers/specs/2026-03-24-yadm-dotfiles-design.md`

---

### Task 1: Install yadm and initialize

**Files:**
- No files created/modified — system setup only

- [ ] **Step 1: Install yadm**

```bash
sudo apt install yadm
```

- [ ] **Step 2: Verify version >= 2.0**

```bash
yadm version
```

Expected: `yadm 3.x.x` (Ubuntu packages yadm 3.x)

- [ ] **Step 3: Initialize yadm**

```bash
yadm init
```

- [ ] **Step 4: Set remote**

```bash
yadm remote add origin git@github.com:billmal071/dotfiles.git
```

- [ ] **Step 5: Archive old repo content**

```bash
cd ~/Documents/personal/dotfiles
git tag archive/symlink-based
git push origin archive/symlink-based
```

---

### Task 2: Create yadm gitignore

**Files:**
- Create: `~/.config/yadm/gitignore`

- [ ] **Step 1: Create the gitignore**

```bash
mkdir -p ~/.config/yadm
cat > ~/.config/yadm/gitignore << 'EOF'
# Neovim generated/cached
.config/nvim/.git/
.config/nvim/venv/
.config/nvim/lazy-lock.json

# Claude Code machine-specific
.claude/settings.local.json
.claude/.credentials.json
.claude/history.jsonl
.claude/projects/
.claude/plugins/
.claude/statsig/
.claude/todos/
.claude/statusline-command.sh

# Shell secrets
.config/shell/secrets.sh

# Agents cache
.agents/skills/

# OS junk
.DS_Store
EOF
```

- [ ] **Step 2: Add to yadm**

```bash
yadm add ~/.config/yadm/gitignore
yadm commit -m "chore: add yadm gitignore"
```

---

### Task 3: Add Ghostty config

**Files:**
- Add: `~/.config/ghostty/config`
- Add: `~/.config/ghostty/glow.glsl`

- [ ] **Step 1: Add files**

```bash
yadm add ~/.config/ghostty/config ~/.config/ghostty/glow.glsl
```

- [ ] **Step 2: Commit**

```bash
yadm commit -m "feat: add ghostty config"
```

---

### Task 4: Add Neovim config

**Files:**
- Add: `~/.config/nvim/init.lua`
- Add: `~/.config/nvim/.stylua.toml`
- Add: `~/.config/nvim/lua/autocmds.lua`
- Add: `~/.config/nvim/lua/chadrc.lua`
- Add: `~/.config/nvim/lua/mappings.lua`
- Add: `~/.config/nvim/lua/options.lua`
- Add: `~/.config/nvim/lua/configs/conform.lua`
- Add: `~/.config/nvim/lua/configs/lazy.lua`
- Add: `~/.config/nvim/lua/configs/lspconfig.lua`
- Add: `~/.config/nvim/lua/plugins/custom.lua`
- Add: `~/.config/nvim/lua/plugins/init.lua`

- [ ] **Step 1: Add all nvim files (gitignore excludes .git, venv, lazy-lock.json)**

```bash
yadm add ~/.config/nvim/init.lua ~/.config/nvim/.stylua.toml
yadm add ~/.config/nvim/lua/autocmds.lua ~/.config/nvim/lua/chadrc.lua
yadm add ~/.config/nvim/lua/mappings.lua ~/.config/nvim/lua/options.lua
yadm add ~/.config/nvim/lua/configs/conform.lua ~/.config/nvim/lua/configs/lazy.lua
yadm add ~/.config/nvim/lua/configs/lspconfig.lua
yadm add ~/.config/nvim/lua/plugins/custom.lua ~/.config/nvim/lua/plugins/init.lua
```

- [ ] **Step 2: Verify no unwanted files staged**

```bash
yadm status
```

Expected: Only the lua files, init.lua, and .stylua.toml staged. No .git/, venv/, or lazy-lock.json.

- [ ] **Step 3: Commit**

```bash
yadm commit -m "feat: add neovim config"
```

---

### Task 5: Add gitconfig (stripped of identity)

**Files:**
- Add: `~/.gitconfig`

- [ ] **Step 1: Remove user identity from gitconfig**

The current `~/.gitconfig` has `[user] email` and `name` set globally. Remove them since they're set per-repo:

```bash
git config --global --unset user.email
git config --global --unset user.name
```

- [ ] **Step 2: Verify gitconfig looks right**

```bash
cat ~/.gitconfig
```

Expected: Should have `[core]`, `[init]`, `[credential]`, `[coderabbit]` sections but no `[user]` section.

- [ ] **Step 3: Add and commit**

```bash
yadm add ~/.gitconfig
yadm commit -m "feat: add gitconfig (no user identity — set per-repo)"
```

---

### Task 6: Create shared shell config and OS-specific rc files

**Files:**
- Create: `~/.config/shell/shared.sh`
- Create: `~/.bashrc##os.Linux` (rename current `~/.bashrc`)
- Create: `~/.zshrc##os.Darwin` (new placeholder for macOS)

**IMPORTANT:** The current `~/.bashrc` contains hardcoded API keys/secrets. These MUST be moved to a separate `~/.config/shell/secrets.sh` that is NOT tracked by yadm.

- [ ] **Step 1: Create secrets file (untracked)**

Extract secrets from current `.bashrc` into an untracked file:

Manually copy the secret exports from your current `~/.bashrc` into a new file:

```bash
mkdir -p ~/.config/shell
nvim ~/.config/shell/secrets.sh
```

Add all `export` lines for API keys (GOOGLE_API_KEY, GROK_API_KEY, MORPH_API_KEY, AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, etc.) from your current `~/.bashrc`. Then lock permissions:

```bash
chmod 600 ~/.config/shell/secrets.sh
```

- [ ] **Step 2: Create shared.sh with portable aliases and exports**

```bash
cat > ~/.config/shell/shared.sh << 'SHARED_EOF'
# Shared shell config — sourced by both .bashrc and .zshrc

# Editor
export EDITOR='nvim'

# PATH additions
export PATH="$HOME/.local/bin:$PATH"
export PATH="$HOME/.cargo/bin:$PATH"
export PATH="$HOME/.gem/bin:$PATH"
export PATH="$PATH:$HOME/go/bin"

# pnpm
export PNPM_HOME="$HOME/.local/share/pnpm"
case ":$PATH:" in
  *":$PNPM_HOME:"*) ;;
  *) export PATH="$PNPM_HOME:$PATH" ;;
esac

# Android SDK
export ANDROID_HOME="$HOME/Android/Sdk"
export PATH="$PATH:$ANDROID_HOME/platform-tools:$ANDROID_HOME/emulator"

# Aliases
alias ll='ls -alF'
alias la='ls -A'
alias l='ls -CF'
alias sp="spotify_player"
alias anime="ani-cli"
alias manga="mangal"
alias mux="tmuxinator"

# Load secrets if present (not tracked by yadm)
[ -f "$HOME/.config/shell/secrets.sh" ] && source "$HOME/.config/shell/secrets.sh"

# fnm (Node version manager)
if command -v fnm &> /dev/null; then
  if [ -n "$ZSH_VERSION" ]; then
    eval "$(fnm env --use-on-cd --shell zsh)"
  elif [ -n "$BASH_VERSION" ]; then
    eval "$(fnm env --use-on-cd --shell bash)"
  fi
fi

# zoxide
if command -v zoxide &> /dev/null; then
  if [ -n "$ZSH_VERSION" ]; then
    eval "$(zoxide init --cmd cd zsh)"
  elif [ -n "$BASH_VERSION" ]; then
    eval "$(zoxide init --cmd cd bash)"
  fi
fi

# fzf
[ -f "$HOME/.fzf.bash" ] && source "$HOME/.fzf.bash"
[ -f "$HOME/.fzf.zsh" ] && source "$HOME/.fzf.zsh"

# Starship prompt (in Ghostty)
if [ "$TERM_PROGRAM" = "ghostty" ]; then
  if [ -n "$ZSH_VERSION" ]; then
    eval "$(starship init zsh)"
  elif [ -n "$BASH_VERSION" ]; then
    eval "$(starship init bash)"
  fi
fi

# SDKMAN
export SDKMAN_DIR="$HOME/.sdkman"
[[ -s "$HOME/.sdkman/bin/sdkman-init.sh" ]] && source "$HOME/.sdkman/bin/sdkman-init.sh"

# The Exception greeting (skip in tmux)
[ -z "$TMUX" ] && [ -x "$HOME/Documents/personal/exception" ] && "$HOME/Documents/personal/exception"
SHARED_EOF
```

- [ ] **Step 3: Create .bashrc##os.Linux**

This replaces the current `~/.bashrc`. Linux-specific stuff stays here, portable stuff moved to shared.sh:

```bash
cat > ~/.bashrc##os.Linux << 'BASH_EOF'
# ~/.bashrc — Linux only (yadm alternate)

# If not running interactively, don't do anything
case $- in
    *i*) ;;
      *) return;;
esac

# History
HISTCONTROL=ignoreboth
shopt -s histappend
HISTSIZE=1000
HISTFILESIZE=2000
shopt -s checkwinsize

# Prompt (fallback if starship not loaded)
if [ -z "${debian_chroot:-}" ] && [ -r /etc/debian_chroot ]; then
    debian_chroot=$(cat /etc/debian_chroot)
fi
case "$TERM" in
    xterm-color|*-256color) color_prompt=yes;;
esac
if [ "$color_prompt" = yes ]; then
    PS1='${debian_chroot:+($debian_chroot)}\[\033[01;32m\]\u@\h\[\033[00m\]:\[\033[01;34m\]\w\[\033[00m\]\$ '
else
    PS1='${debian_chroot:+($debian_chroot)}\u@\h:\w\$ '
fi
unset color_prompt

# Linux color support
if [ -x /usr/bin/dircolors ]; then
    test -r ~/.dircolors && eval "$(dircolors -b ~/.dircolors)" || eval "$(dircolors -b)"
    alias grep='grep --color=auto'
fi

# Bash completion
if ! shopt -oq posix; then
  if [ -f /usr/share/bash-completion/bash_completion ]; then
    . /usr/share/bash-completion/bash_completion
  elif [ -f /etc/bash_completion ]; then
    . /etc/bash_completion
  fi
fi

# Linux-specific: Bluetooth audio profile switchers
alias btsbc="pactl set-card-profile bluez_card.6C_12_70_30_FE_91 a2dp-sink"
alias btxq="pactl set-card-profile bluez_card.6C_12_70_30_FE_91 a2dp-sink-sbc_xq"
alias bthf="pactl set-card-profile bluez_card.6C_12_70_30_FE_91 headset-head-unit"

# PulseAudio
export PULSE_PROP="media.role=music"
export PULSE_LATENCY_MSEC=60
if [ -n "$TMUX" ]; then
    export PULSE_PROP="media.role=music"
fi

# Linux-specific: OpenSSL custom build
export PATH="/usr/local/openssl/bin:$PATH"
export LD_LIBRARY_PATH="/usr/local/openssl/lib:$LD_LIBRARY_PATH"
export PKG_CONFIG_PATH="/usr/local/openssl/lib/pkgconfig:$PKG_CONFIG_PATH"

# Linux-specific aliases
alias exception='~/Documents/personal/exception'
alias aionui="/opt/AionUi/AionUi"
alias dashboard="uv run job-agent dashboard"

EAS_SKIP_AUTO_FINGERPRINT=1

# Source shared config
[ -f "$HOME/.config/shell/shared.sh" ] && source "$HOME/.config/shell/shared.sh"
BASH_EOF
```

- [ ] **Step 4: Create .zshrc##os.Darwin (macOS placeholder)**

```bash
cat > ~/.zshrc##os.Darwin << 'ZSH_EOF'
# ~/.zshrc — macOS only (yadm alternate)

# History
HISTSIZE=1000
SAVEHIST=2000
HISTFILE=~/.zsh_history
setopt HIST_IGNORE_DUPS APPEND_HISTORY SHARE_HISTORY

# macOS-specific aliases
alias ls='ls -G'
alias grep='grep --color=auto'

# Homebrew (Apple Silicon path)
if [ -f /opt/homebrew/bin/brew ]; then
    eval "$(/opt/homebrew/bin/brew shellenv)"
fi

# Source shared config
[ -f "$HOME/.config/shell/shared.sh" ] && source "$HOME/.config/shell/shared.sh"
ZSH_EOF
```

- [ ] **Step 5: Back up existing .bashrc and add files to yadm**

```bash
cp ~/.bashrc ~/.bashrc.bak
yadm add ~/.config/shell/shared.sh
yadm add ~/.bashrc##os.Linux
yadm add ~/.zshrc##os.Darwin
```

- [ ] **Step 6: Run yadm alt to create the alternate links**

```bash
yadm alt
```

Expected: `~/.bashrc` now links to `~/.bashrc##os.Linux` on this Linux machine.

- [ ] **Step 7: Verify shell still works**

Open a new terminal or run:

```bash
bash --login
```

Verify prompt loads, aliases work (`ll`, `sp`), and no errors.

- [ ] **Step 8: Commit**

```bash
yadm commit -m "feat: add shell configs with OS alternates and shared.sh"
```

---

### Task 7: Add Claude Code configs

**Files:**
- Add: `~/.claude/settings.json`
- Add: `~/.claude/CLAUDE.md`

**Note:** `settings.json` has a hardcoded path in `statusLine.command` (`bash /home/williams/.claude/statusline-command.sh`). This will work on both machines if the username is `williams`. If the Mac uses a different username, this will need a path update later.

- [ ] **Step 1: Replace symlinks with real files**

The current `~/.claude/settings.json` and `~/.claude/CLAUDE.md` may be symlinks to the old dotfiles dir. Replace them with real files:

```bash
for f in ~/.claude/settings.json ~/.claude/CLAUDE.md; do
  if [ -L "$f" ]; then
    target=$(readlink -f "$f")
    rm "$f"
    cp "$target" "$f"
  fi
done
```

- [ ] **Step 2: Review CLAUDE.md for sensitive content**

```bash
cat ~/.claude/CLAUDE.md
```

Current content is just `- Add to memory` — safe to commit.

- [ ] **Step 3: Add and commit**

```bash
yadm add ~/.claude/settings.json ~/.claude/CLAUDE.md
yadm commit -m "feat: add claude code config"
```

---

### Task 8: Add agents skill lock

**Files:**
- Add: `~/.agents/.skill-lock.json`

- [ ] **Step 1: Fix broken symlink**

The current `~/.agents/.skill-lock.json` is a broken symlink pointing to the old `claude-dotfiles` path. Replace it with the actual file:

```bash
rm ~/.agents/.skill-lock.json
cp ~/Documents/personal/dotfiles/agents/.skill-lock.json ~/.agents/.skill-lock.json
```

- [ ] **Step 2: Add and commit**

```bash
yadm add ~/.agents/.skill-lock.json
yadm commit -m "feat: add agents skill lock"
```

---

### Task 9: Add SSH config

**Files:**
- Add: `~/.ssh/config`

**Note:** Only track the config file. Private keys, known_hosts, and authorized_keys are machine-specific and must NOT be tracked.

- [ ] **Step 1: Add SSH ignores to yadm gitignore**

```bash
cat >> ~/.config/yadm/gitignore << 'EOF'

# SSH — only track config, ignore keys and state
.ssh/id_*
.ssh/*.pub
.ssh/aws_server_key*
.ssh/kiwiapi_digitalocean*
.ssh/kiwiport*
.ssh/kwikport_prod*
.ssh/fetchit*
.ssh/storytime_rsa*
.ssh/known_hosts*
.ssh/authorized_keys
.ssh/last
EOF
yadm add ~/.config/yadm/gitignore
```

- [ ] **Step 2: Add SSH config and commit**

```bash
yadm add ~/.ssh/config
yadm commit -m "feat: add ssh config"
```

---

### Task 10: Create bootstrap script

**Files:**
- Create: `~/.config/yadm/bootstrap`

- [ ] **Step 1: Create bootstrap**

```bash
cat > ~/.config/yadm/bootstrap << 'BOOT_EOF'
#!/bin/bash
# Runs automatically after yadm clone on a new machine

echo "Bootstrapping dotfiles..."

# Install skills if npx is available
if command -v npx &> /dev/null; then
    echo "Installing Claude Code skills..."
    npx skills install
fi

# macOS-specific setup
if [ "$(uname)" = "Darwin" ]; then
    if command -v brew &> /dev/null; then
        echo "Homebrew detected — install packages as needed"
        # Uncomment and add packages:
        # brew install ghostty neovim starship zoxide fnm fzf tmuxinator
    fi
fi

echo "Bootstrap complete!"
echo ""
echo "NOTE: Create ~/.config/shell/secrets.sh with your API keys (not tracked by yadm)"
BOOT_EOF
chmod +x ~/.config/yadm/bootstrap
```

- [ ] **Step 2: Add and commit**

```bash
yadm add ~/.config/yadm/bootstrap
yadm commit -m "feat: add bootstrap script"
```

---

### Task 11: Force push to remote and clean up

- [ ] **Step 1: Push to remote**

```bash
yadm push --force -u origin main
```

- [ ] **Step 2: Delete old dotfiles directory**

```bash
rm -rf ~/Documents/personal/dotfiles
```

- [ ] **Step 3: Verify everything works**

```bash
yadm status
yadm list
```

Expected: Clean status, all tracked files listed.

- [ ] **Step 4: Final commit if any cleanup needed**

```bash
yadm status
# If anything needs committing:
# yadm commit -m "chore: cleanup after migration"
```
