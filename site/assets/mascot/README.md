# lobster mascot

Marlowe is an original character drawn for this project: a coral-red lobster, friendly and a little smug, a film-night regular who sits in front of a terminal. All artwork is hand-authored SVG. Nothing is traced, copied or generated, and no third-party mascot or brand was used as a reference.

## Files

| File | Use |
| --- | --- |
| `mascot.svg` | Main character, full body, waving with a remote. 400x400. |
| `mascot-watching.svg` | Watching a film with a popcorn tub. |
| `mascot-thumbs-up.svg` | Winking, thumbs-up claw, green check on the screen ("doctor says ok"). |
| `mascot-shrug.svg` | Shrugging, question mark on the screen. Used by `site/404.html`; fits errors and empty states. |
| `mascot-mark.svg` | Head, claws, antennae and tail on a transparent background. Nav logo. |
| `mascot-icon.svg` | The mark on a dark rounded square. App-icon sizes (180px). |
| `../../favicon.svg` | Simplest crop (no tail, no mouth, heavier outline) for 16 and 32px. |
| `../../apple-touch-icon.png` | 180px render of `mascot-icon.svg` (iOS ignores SVG icons). |
| `mascot-mono.svg` | One-colour line art. The colour is the `color` attribute on the root element; every stroke and fill uses `currentColor`. |
| `social-preview.png` | 1280x640 GitHub social preview (alt text: "Marlowe, the lobster mascot, beside the lobster wordmark and tagline"), rendered from `mascot.svg` with headless Chrome. Upload it under repository Settings, General, Social preview. There is no API for this. |
| `lobster.ans`, `lobster.txt` | Terminal lobster: 10 rows, 24 columns. `.ans` uses 256-colour escape codes (coral 203, dark coral 167, cream 230); `.txt` is the no-colour fallback and uses distinct glyphs for eyes and mouth so it still reads. |

Palette: shell `#e9533f`, shadow `#b93a2c`, highlight `#f78a74`, outline `#2a1512`, cream `#fff3df`. The outline keeps the shell readable on cream; on near-black backgrounds the coral carries the shape. Every SVG has a `<title>` and `<desc>`, a `viewBox`, no external references and no fonts.

The hero on the landing page is the cropped `mascot.svg` construction (head, antennae, claws, remote) inlined into `index.html` so `style.css` can animate the blink (`.lid`) and the wave (`.wave`). Both animations are off under `prefers-reduced-motion`.

## Name

The mascot is called **Marlowe**: a noir-detective name for a lobster who is always at the late show and always a step ahead of the plot.

## Licence

The artwork is original and released under the same licence as the repository, GNU GPL version 2 (see `LICENSE` at the repository root). Keep the licence notice if you redistribute it.

## Terminal use (proposal for a follow-up Go change)

Nothing here changes Go code. Suggested wiring:

- `lobster version` and the `--help` banner: print the small mark above the text, only when stdout is a TTY.
- `lobster doctor` header: the thumbs-up lobster when all sources are ok, the shrug when none are.
- Empty-state and not-found messages: one line with the shrug and the message.
- Colour: use `lobster.ans` only when stdout is a terminal, `NO_COLOR` is unset and `TERM` is not `dumb`; use `lobster.txt` when colour is off but the terminal is interactive; print nothing when output is piped or `--json` is in effect.
- Embed both files with `go:embed` so the binary needs no extra files.
