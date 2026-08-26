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
PRELOAD_FLAGS = {"-r", "--require", "--import", "--loader", "--experimental-loader"}

# Flags that consume the next token as a module name; anything after that is
# argv for the module, not something the interpreter executes. Scanning past
# these would flag `python3 -m pip install pkg.whl`.
MODULE_FLAGS = {"-m", "--module"}

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


def targets(segment):
    """Yield the file target of each interpreter invocation in one segment."""
    # Quotes are delimiters, not grouping: the loader hid its command inside a
    # JSON string, so treating a quoted run as one atom would hide it.
    tokens = segment.replace('"', " ").replace("'", " ").split()
    for i, tok in enumerate(tokens):
        if os.path.basename(tok) not in INTERPRETERS:
            continue
        rest = tokens[i + 1:]
        j = 0
        while j < len(rest):
            nxt = rest[j]
            j += 1
            if nxt in INLINE_FLAGS:
                break                      # inline code, no file target
            if nxt in MODULE_FLAGS:
                break                      # module name, then its own argv
            if nxt in PRELOAD_FLAGS:
                if j < len(rest):          # classify the preloaded file too...
                    yield rest[j].strip(",;:()[]{}")
                    j += 1
                continue                   # ...then keep looking for the program
            if nxt.startswith("-"):
                # --require=./payload.txt binds the operand with '='.
                flag, _, value = nxt.partition("=")
                if value and flag in PRELOAD_FLAGS:
                    yield value.strip(",;:()[]{}")
                continue                   # unrelated flag
            if nxt.startswith((">", "<", "2>", "&")):
                continue                   # redirection
            # Shell and JSON punctuation clinging to a token is not part of the
            # path. Stripping it also means `"type": "node",` yields nothing,
            # rather than reporting ',' as a target.
            nxt = nxt.strip(",;:()[]{}")
            if not nxt:
                continue
            yield nxt
            break


def suspicious(target, path):
    base = os.path.basename(target).strip(",;:()[]{}")
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

    for root, dirs, _ in os.walk("."):
        dirs[:] = [d for d in dirs if d not in SKIP_DIRS or d == ".vscode"]
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
