# Centralized Dotfiles with yadm

## Overview

Migrate from a symlink-based dotfiles repo to yadm (>= 2.0), which tracks dotfiles in-place using Git. This enables portable configuration across a Linux desktop and a macOS MacBook with zero symlinks and OS-aware file alternates.

## Prerequisites

- **yadm >= 2.0** (alternate file syntax `##os.<OS>` requires 2.0+)
- **SSH key configured on GitHub** for `git@github.com` clone, or use HTTPS fallback: `https://github.com/billmal071/dotfiles.git`

## Goals

- Single `yadm clone` sets up a new machine
- OS-specific shell configs (bash on Linux, zsh on macOS) with shared customizations
- Easy to extend — adding a new config is just `yadm add` and `yadm commit`
- Migrate existing Claude Code configs from the current symlink-based repo

## Tracked Configs

| Config | Files | Notes |
|--------|-------|-------|
| Ghostty | `~/.config/ghostty/config`, `~/.config/ghostty/glow.glsl` | Same on both platforms |
| Neovim | `~/.config/nvim/init.lua`, `~/.config/nvim/lua/`, `~/.config/nvim/.stylua.toml` | Add files explicitly; see gitignore for exclusions |
| Git | `~/.gitconfig` | Shared settings only (no user.name/email — set per-repo) |
| Shell (Linux) | `~/.bashrc##os.Linux` | Sources shared.sh |
| Shell (macOS) | `~/.zshrc##os.Darwin` | Sources shared.sh |
| Shell (shared) | `~/.config/shell/shared.sh` | Aliases, exports, PATH — sourced by both shells |
| Claude Code | `~/.claude/settings.json`, `~/.claude/CLAUDE.md` | Review CLAUDE.md for sensitive content before committing |
| Agents | `~/.agents/.skill-lock.json` | Intentionally tracked to sync installed skills across machines (unlike lazy-lock.json, this requires manual curation) |

## yadm Gitignore

Create `~/.config/yadm/gitignore` to prevent accidentally staging unwanted files:

All paths are relative to `$HOME` with no leading slash:

```gitignore
# Neovim generated/cached
# Note: nvim/.git exists because it was cloned from NvChad/starter
.config/nvim/.git
.config/nvim/venv/
.config/nvim/lazy-lock.json

# Claude Code machine-specific
.claude/settings.local.json
.claude/.credentials.json
.claude/history.jsonl
```

This is respected by `yadm status` and `yadm add`.

## Cross-Platform Shell Strategy

yadm alternate files use the `##os.<OS>` suffix (where OS comes from `uname -s`) to deliver OS-specific files:

- `~/.bashrc##os.Linux` — deployed as `~/.bashrc` on Linux only
- `~/.zshrc##os.Darwin` — deployed as `~/.zshrc` on macOS only

Shared shell customizations (aliases, exports, PATH) live in `~/.config/shell/shared.sh` and are sourced by both rc files:

```bash
# At the end of .bashrc / .zshrc
[ -f "$HOME/.config/shell/shared.sh" ] && source "$HOME/.config/shell/shared.sh"
```

## Migration Plan

### Phase 1: Install yadm and initialize

1. Install yadm on Linux (`sudo apt install yadm` — verify version >= 2.0 with `yadm version`)
2. `yadm init` in home directory
3. `yadm remote add origin git@github.com:billmal071/dotfiles.git`
4. Archive old repo content: `git -C ~/Documents/personal/dotfiles tag archive/symlink-based && git -C ~/Documents/personal/dotfiles push origin archive/symlink-based`
5. `yadm push --force -u origin main` — replaces old repo content. The archive tag preserves old history if needed.

### Phase 2: Add configs

1. Create `~/.config/yadm/gitignore` with exclusion rules
2. Add Ghostty config files
3. Add Neovim config files explicitly (`yadm add` each file/directory, gitignore handles exclusions)
4. Add `.gitconfig` (strip any user.name/email if present)
5. Create `~/.config/shell/shared.sh` with shared aliases/exports extracted from current `.bashrc`
6. Create alternate files: `.bashrc##os.Linux`, `.zshrc##os.Darwin` — each sources `shared.sh`
7. Run `yadm alt` to generate the alternate file links (automatic on clone, but needed when adding alternates in-place)
8. Add existing Claude Code configs (`settings.json`, `CLAUDE.md` — review CLAUDE.md for private content; strip anything sensitive or keep it in an untracked `CLAUDE.local.md` sourced separately)
9. Add agents skill lock file (managed by Claude Code's `npx skills` — tracked to sync skill versions across machines)

### Phase 3: Bootstrap script

Create `~/.config/yadm/bootstrap` and **mark it executable** (`chmod +x`):

```bash
#!/bin/bash
# Runs automatically after yadm clone on a new machine

# Install skills if npx is available
if command -v npx &> /dev/null; then
    echo "Installing skills..."
    npx skills install
fi

# macOS-specific setup
if [ "$(uname)" = "Darwin" ]; then
    # Placeholder: add brew install commands as needed, e.g.:
    # brew install ghostty neovim
    if command -v brew &> /dev/null; then
        echo "Homebrew detected — add packages to bootstrap as needed"
    fi
fi

echo "Bootstrap complete!"
```

### Phase 4: Cleanup

1. Remove symlinks that pointed to old `~/Documents/personal/dotfiles`
2. Delete `~/Documents/personal/dotfiles` directory
3. Verify configs work correctly from their in-place locations

## New Machine Setup

```bash
# macOS
brew install yadm
yadm clone git@github.com:billmal071/dotfiles.git
# bootstrap runs automatically

# Linux (Ubuntu/Debian)
sudo apt install yadm
yadm clone git@github.com:billmal071/dotfiles.git
```

**Handling existing files:** If the target machine already has files that conflict (e.g., macOS default `~/.zshrc`), `yadm clone` will warn but not overwrite. Back up conflicting files, then run `yadm checkout -- .` from `$HOME` to check out all tracked files.

**Note:** `yadm clone` automatically runs `yadm alt` (to create alternate file links) and `yadm bootstrap` (to run the bootstrap script) in yadm >= 2.0. No manual steps needed.

## Adding New Configs Later

```bash
yadm add ~/.config/some-tool/config
yadm commit -m "add some-tool config"
yadm push
```

For OS-specific files, use the alternate suffix:
```bash
yadm add ~/.some-config##os.Darwin
yadm add ~/.some-config##os.Linux
yadm commit -m "add OS-specific config"
```

New files added under already-tracked directories (e.g., new lua files in nvim) must be explicitly staged with `yadm add`.

## What NOT to Track

- Credentials, tokens, secrets (`.credentials.json`, `gh` auth)
- Machine-specific settings (`settings.local.json`)
- Generated/cached files (`lazy-lock.json`, `venv/`, `node_modules/`)
- Application state (`dconf`, Chrome profiles, Docker Desktop)
