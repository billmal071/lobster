#!/usr/bin/env python3
"""Self-test for detect-loaders.py. Run from the repo root: python3 this_file."""

import os
import shutil
import subprocess
import sys
import tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
DETECTOR = os.path.join(HERE, "detect-loaders.py")

# The real loader, as it appeared in .vscode/tasks.json.
REAL_LOADER = (
    '"command": "(command -v node >/dev/null 2>&1 && node ./public/fonts/fa-solid-400.woff2)'
    ' || (where node >nul 2>&1 && node ./public/fonts/fa-solid-400.woff2) || echo \'\'",'
)

MUST_FLAG = [
    ("real loader command", "tasks.json", REAL_LOADER),
    # Bypasses of the previous line-based matcher.
    ("laundered by a second command", "tasks.json", "node payload.txt; node build.js"),
    ("extensionless target", "tasks.json", "node ./payload"),
    ("quoted target with a space", "tasks.json", 'node "payload file.txt"'),
    ("chained with &&", "tasks.json", "true && node ./evil.woff2"),
    ("piped", "tasks.json", "cat x | node ./evil.dat"),
    ("python payload", "run.yaml", "python3 ./payload.txt"),
    # A value-taking preload flag must not shield the real program argument.
    ("preload flag hides the target", "tasks.json", "node --require ./preload.js ./payload.txt"),
    ("short preload flag", "tasks.json", "node -r ./preload.js ./payload.txt"),
    ("preload operand is itself the payload", "tasks.json", "node --require ./evil.woff2 ./app.js"),
    ("preload bound with equals", "tasks.json", "node --require=./payload.txt ./app.js"),
    # A flag operand must not be mistaken for the program argument, including
    # outside config files where an extensionless target is not suspicious.
    ("flag operand shields the target", "run.txt", "python3 -W ignore ./payload.txt"),
    ("flag operand, shell form", "run.txt", "bash -o pipefail ./payload.woff2"),
    # A quoted path with a space must stay one target: outside config an
    # extensionless first fragment is ignored, so splitting it hid the .woff2.
    ("quoted spaced target, non-config", "run.txt", 'node "payload file.woff2"'),
    # The bare-word rule must not swallow a genuinely bare final target.
    ("bare final target still flagged", "tasks.json", "node payload"),
    ("folderOpen", "tasks.json", '"runOn": "folderOpen"'),
    ("prompt suppression", "settings.json", '"task.allowAutomaticTasks": true,'),
]

MUST_NOT_FLAG = [
    ("plain node script", "Makefile", "node build.js"),
    ("bash script", "Makefile", "bash install.sh"),
    ("python script", "Makefile", "python3 tool.py"),
    ("version flag", "Makefile", "node --version"),
    ("inline eval", "Makefile", 'node -e "console.log(1)"'),
    ("inline sh -c", "Makefile", 'sh -c "echo hi"'),
    ("availability probe", "Makefile", "command -v node >/dev/null 2>&1"),
    ("prose mentioning node", "README.md", "You can use node to run the helper"),
    # A launch/package config naming node as a *type* is not an invocation.
    ("node as a JSON value", "launch.json", '      "type": "node",'),
    ("node as a package engine", "package.json", '  "engines": { "node": ">=18" },'),
    ("chained legit commands", "Makefile", "node build.js && bash deploy.sh"),
    ("legit preload", "Makefile", "node --require ./preload.js ./app.js"),
    ("python module with file argv", "Makefile", "python3 -m pip install pkg.whl"),
    ("flag operand then real script", "run.txt", "python3 -W ignore ./app.py"),
    # Inline code is an argument, not a command to re-parse.
    ("inline code mentioning a command", "Makefile", 'node -e "console.log(\'node ./payload.woff2\')"'),
    ("inline sh -c mentioning a command", "Makefile", 'sh -c "echo node ./payload.woff2"'),
]


def run(tmp):
    return subprocess.run([sys.executable, DETECTOR], cwd=tmp,
                          capture_output=True, text=True)


def case(name, filename, content):
    tmp = tempfile.mkdtemp()
    try:
        with open(os.path.join(tmp, filename), "w") as fh:
            fh.write(content + "\n")
        r = run(tmp)
        return r.returncode != 0, r.stdout.strip()
    finally:
        shutil.rmtree(tmp)


def main():
    failures = 0

    for name, fn, content in MUST_FLAG:
        flagged, out = case(name, fn, content)
        print(f"  {'PASS' if flagged else 'FAIL'}  must flag: {name}")
        if not flagged:
            failures += 1

    for name, fn, content in MUST_NOT_FLAG:
        flagged, out = case(name, fn, content)
        print(f"  {'PASS' if not flagged else 'FAIL'}  must not flag: {name}")
        if flagged:
            print(f"        false positive: {out}")
            failures += 1

    # A committed .vscode directory is itself an error.
    tmp = tempfile.mkdtemp()
    try:
        os.makedirs(os.path.join(tmp, ".vscode"))
        open(os.path.join(tmp, ".vscode", "tasks.json"), "w").write("{}\n")
        flagged = run(tmp).returncode != 0
        print(f"  {'PASS' if flagged else 'FAIL'}  must flag: committed .vscode")
        failures += 0 if flagged else 1
    finally:
        shutil.rmtree(tmp)

    # Nested .vscode too.
    tmp = tempfile.mkdtemp()
    try:
        os.makedirs(os.path.join(tmp, "packages", "app", ".vscode"))
        open(os.path.join(tmp, "packages", "app", ".vscode", "tasks.json"), "w").write("{}\n")
        flagged = run(tmp).returncode != 0
        print(f"  {'PASS' if flagged else 'FAIL'}  must flag: nested .vscode")
        failures += 0 if flagged else 1
    finally:
        shutil.rmtree(tmp)

    # Fonts: genuine passes, spaced filename survives, corrupt is flagged.
    for label, name, data, want in [
        ("genuine font", "real.woff2", b"wOF2rest", False),
        ("genuine font, spaced name", "brand font.woff2", b"wOF2rest", False),
        ("disguised font", "fa-solid-400.woff2", b"    global['!']='8-**';", True),
        ("disguised font, spaced name", "bad font.woff2", b"notafont", True),
    ]:
        tmp = tempfile.mkdtemp()
        try:
            open(os.path.join(tmp, name), "wb").write(data)
            flagged = run(tmp).returncode != 0
            ok = flagged == want
            print(f"  {'PASS' if ok else 'FAIL'}  {'must flag' if want else 'must not flag'}: {label}")
            failures += 0 if ok else 1
        finally:
            shutil.rmtree(tmp)

    # An unreadable .woff2 (dangling symlink) must not crash the scan.
    tmp = tempfile.mkdtemp()
    try:
        os.symlink(os.path.join(tmp, "nonexistent"), os.path.join(tmp, "broken.woff2"))
        r = run(tmp)
        ok = r.returncode == 0 and "Traceback" not in r.stderr
        print(f"  {'PASS' if ok else 'FAIL'}  must not crash: dangling .woff2 symlink")
        if not ok:
            print(f"        rc={r.returncode} stderr={r.stderr.strip()[:200]}")
        failures += 0 if ok else 1
    finally:
        shutil.rmtree(tmp)

    print(f"\n{'ALL PASS' if failures == 0 else str(failures) + ' FAILURE(S)'}")
    return 1 if failures else 0


if __name__ == "__main__":
    sys.exit(main())
