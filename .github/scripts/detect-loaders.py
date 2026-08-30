#!/usr/bin/env python3
"""Detect auto-execution loaders and payloads disguised as data files.

The repo previously carried a VS Code task that ran on folder open and executed
a JavaScript payload named fa-solid-400.woff2. This scans for that shape:

  1. a committed .vscode directory (anywhere in the tree)
  2. a task configured to run on folder open
  3. task.allowAutomaticTasks:true, which suppresses the run prompt
  4. an interpreter invoked against something that is not a known script
  5. a .woff2 without the wOF2 magic bytes

Check 4 parses each interpreter invocation separately rather than matching whole
lines. A line-based matcher is trivially bypassed by appending an innocuous
second command ("node payload.txt; node build.js"), and misses extensionless and
quoted targets.
"""

import os
import re
import sys

SKIP_DIRS = {".git", "node_modules", ".github", "vendor"}

# Extensions that legitimately follow an interpreter.
SCRIPT_EXT = {
    "js", "mjs", "cjs", "jsx", "ts", "tsx", "py", "sh", "bash", "zsh",
    "rb", "pl", "lua", "ps1", "bat", "cmd",
}

INTERPRETERS = {
    "node", "python", "python3", "deno", "bun", "ruby", "perl",
    "sh", "bash", "zsh",
}

# Flags whose argument is inline code, not a file to execute.
INLINE_FLAGS = {"-c", "-e", "--eval", "--exec", "-p", "--print"}

# Flags that consume the next token AND execute it, then still run a program
# argument afterwards. `node --require ./preload.js ./payload.txt` executes
# both, so both have to be classified — skipping the flag and stopping at
# ./preload.js let ./payload.txt through unexamined.
PRELOAD_FLAGS = {"-r", "--require", "--import", "--loader", "--experimental-loader",
                 # Shells source these before the program argument, so the same
                 # rule applies: the operand is path-shaped, which means the
                 # "bare word after a flag" heuristic below cannot recognize it
                 # and the scan would stop at an innocuous ./preload.sh without
                 # ever reaching the payload behind it.
                 "--rcfile", "--init-file"}

# Interpreters are routinely installed under versioned names (python3.12,
# node20). Matching only the bare name lets those walk straight past, so strip a
# trailing version before the lookup. The stem must differ from the basename,
# which is what keeps "nodemon" from resolving to "node".
VERSION_SUFFIX = re.compile(r"[0-9]+(?:\.[0-9]+)*$")

# Flags that consume the next token as a module name; anything after that is
# argv for the module, not something the interpreter executes. Scanning past
# these would flag `python3 -m pip install pkg.whl`.
MODULE_FLAGS = {"-m", "--module"}

# Shell grouping and command-substitution punctuation clings to the front of the
# interpreter name: `(node ./x)` and `$(node ./x)` tokenize as "(node"/"$(node".
GROUPING = "(){}`$"

# Punctuation that clings to a path in shell, JSON and prose. A markdown
# sentence ends `...statusline-command.sh`). — strip that and the target is an
# ordinary .sh; leave it and the extension reads as empty, i.e. suspicious.
TRIM = ",;:()[]{}`\"'."

# Words that precede a command without being one. Skipping them keeps
# `sudo node ./payload` in command position; stopping at anything else is what
# keeps prose out.
COMMAND_WRAPPERS = {"sudo", "env", "exec", "nohup", "time", "command",
                    "then", "do", "else"}

# Shell command separators. Splitting on these is what stops a trailing
# "; node build.js" from laundering the whole line.
SEPARATORS = re.compile(r"&&|\|\||[;|&\n]")

# Files where an extensionless interpreter target ("node payload") is
# suspicious. Prose may say "run node to start" and mean nothing by it.
CONFIG_SUFFIX = (".json", ".yaml", ".yml", ".toml", ".sh", ".bash", ".cfg",
                 ".conf", ".ini", ".mk")
CONFIG_NAMES = {"Makefile", "Dockerfile", "makefile", "justfile", "Taskfile"}

MAX_BYTES = 2 << 20


def is_config(path):
    return path.endswith(CONFIG_SUFFIX) or os.path.basename(path) in CONFIG_NAMES


# A quoted run is either a path that happens to contain spaces, or a whole
# shell command embedded in config (which is how the loader hid itself inside
# tasks.json). Tokenizing has to keep both possible, so quotes are preserved
# here and resolved by context below.
TOKEN_RE = re.compile(r"""\"([^"]*)\"|'([^']*)'|(\S+)""")


def tokenize(segment):
    """Yield (text, was_quoted) for each token, keeping quoted runs intact."""
    for m in TOKEN_RE.finditer(segment):
        dq, sq, bare = m.groups()
        if dq is not None:
            yield dq, True
        elif sq is not None:
            yield sq, True
        else:
            yield bare, False


def is_interpreter(token):
    """True if token names an interpreter, including a versioned build."""
    base = os.path.basename(token.lstrip(GROUPING))
    if base in INTERPRETERS:
        return True
    stem = VERSION_SUFFIX.sub("", base)
    return stem != base and stem in INTERPRETERS


def embeds_command(text):
    """True if a quoted run is itself a shell command rather than a path.

    The interpreter has to stand in *command position* — the first real word of
    the run or of one of its separator-delimited segments. Merely containing the
    word is what made `"description": "Use node to run the helper"` recurse, and
    `targets()` then reported `to` as node's target: a false positive that fails
    the CI gate on ordinary package metadata.
    """
    for segment in SEPARATORS.split(text):
        words = segment.split()
        if len(words) < 2:
            continue                    # a lone word is a path, not a command
        for word in words:
            # `VAR=1 node ./x` and `sudo node ./x` still run node.
            if "=" in word and not word.startswith("-"):
                continue
            if os.path.basename(word.strip(GROUPING)) in COMMAND_WRAPPERS:
                continue
            if is_interpreter(word):
                return True
            break                       # first real word is not an interpreter
    return False


def targets(segment, depth=0):
    """Yield the file target of each interpreter invocation in one segment."""
    toks = list(tokenize(segment))
    i = 0
    while i < len(toks):
        text, quoted = toks[i]

        # A quoted run holding a command is config-embedded shell: recurse so
        # the loader's `node ./x.woff2` inside a JSON string is still seen.
        if quoted and depth < 3 and embeds_command(text):
            for sub in SEPARATORS.split(text):
                yield from targets(sub, depth + 1)
            i += 1
            continue

        if not is_interpreter(text):
            i += 1
            continue

        # `"node": "…"` is a JSON key naming a runtime, not an invocation.
        if i + 1 < len(toks) and toks[i + 1][0] == ":":
            i += 1
            continue

        j = i + 1
        while j < len(toks):
            nxt, nxt_quoted = toks[j]
            j += 1
            if nxt in INLINE_FLAGS or nxt in MODULE_FLAGS:
                # The argument is inline code or a module name, not a file.
                # Stop the whole segment: separators are already split out, so
                # everything after this belongs to the inline argument. Scanning
                # on would re-read `node -e "... node ./x.woff2 ..."` as a
                # second invocation and report a false positive.
                return
            if nxt in PRELOAD_FLAGS:
                if j < len(toks):          # classify the preloaded file too...
                    yield toks[j][0].strip(TRIM)
                    j += 1
                continue                   # ...then keep looking for the program
            if not nxt_quoted and nxt.startswith("-"):
                # --require=./payload.txt binds the operand with '='.
                flag, _, value = nxt.partition("=")
                if value and flag in PRELOAD_FLAGS:
                    yield value.strip(TRIM)
                continue                   # unrelated flag
            if nxt.startswith((">", "<", "2>", "&")):
                continue                   # redirection or comparison operator
            # A bare word straight after a flag, with more tokens to come, is
            # that flag's operand rather than the program: `python3 -W ignore
            # ./payload.txt`. Enumerating every value-taking flag across five
            # interpreters is a losing game, so key off the shape instead. A
            # program argument is path-like, or it is the final token.
            if (not nxt_quoted and j - 2 >= 0 and toks[j - 2][0].startswith("-")
                    and "/" not in nxt and "." not in nxt and j < len(toks)):
                continue
            # Shell and JSON punctuation clinging to a token is not part of the
            # path. Stripping it also means `"type": "node",` yields nothing,
            # rather than reporting ',' as a target.
            nxt = nxt.strip(TRIM)
            if not nxt:
                continue
            yield nxt
            break
        i = max(j, i + 1)


def suspicious(target, path):
    base = os.path.basename(target).strip(TRIM)
    if "." not in base:
        # Extensionless targets are the obvious evasion ("node payload"), but in
        # prose "use node to build" is harmless — so only flag them inside
        # config, where an interpreter invocation is always literal. Build-system
        # variables are not literal targets either.
        if target.startswith("$") or "{{" in target or "%" in target:
            return False
        return is_config(path)
    return base.rsplit(".", 1)[1].lower() not in SCRIPT_EXT


def walk():
    for root, dirs, files in os.walk("."):
        dirs[:] = [d for d in dirs if d not in SKIP_DIRS]
        for name in files:
            yield os.path.join(root, name)


def main():
    errors = []

    # Discovery deliberately does NOT use SKIP_DIRS. Pruning .github, vendor or
    # node_modules here hid any .vscode inside them, and an editor will load
    # tasks from those directories just as readily when one is opened as a
    # workspace folder — the content scan skipping a directory is a
    # false-positive concession, not a statement that nothing there can run.
    for root, dirs, _ in os.walk("."):
        dirs[:] = [d for d in dirs if d != ".git"]
        for d in list(dirs):
            if d == ".vscode":
                errors.append(f"{os.path.join(root, d)} is committed; "
                              f".vscode is gitignored (auto-run task vector)")
                dirs.remove(d)

    for path in walk():
        if path.lower().endswith(".woff2"):
            # A dangling symlink or an unreadable path must not abort the whole
            # scan with a traceback — the text branch below already tolerates
            # this. An unreadable file holds no payload anything can execute.
            try:
                with open(path, "rb") as fh:
                    magic = fh.read(4)
            except OSError:
                continue
            if magic != b"wOF2":
                errors.append(f"{path} lacks the wOF2 magic bytes — not a real font")
            continue
        try:
            if os.path.getsize(path) > MAX_BYTES:
                continue
            with open(path, "r", encoding="utf-8", errors="ignore") as fh:
                text = fh.read()
        except OSError:
            continue

        for lineno, line in enumerate(text.splitlines(), 1):
            where = f"{path}:{lineno}"
            if "folderOpen" in line:
                errors.append(f"{where}: runs on folder open")
            if re.search(r'"task\.allowAutomaticTasks"\s*:\s*true', line):
                errors.append(f"{where}: task.allowAutomaticTasks:true suppresses the run prompt")
            for segment in SEPARATORS.split(line):
                for target in targets(segment):
                    if suspicious(target, path):
                        errors.append(f"{where}: interpreter invoked on non-script target {target!r}")

    for e in errors:
        print(f"::error::{e}")
    if errors:
        return 1
    print("Clean — no auto-execution loader, prompt suppression, or disguised payload.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
