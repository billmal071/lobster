# Poster Hero-Band — Manual Test Matrix

Run `go run .` and exercise each cell. ✅/❌ per terminal.

| Scenario | iTerm2 | WezTerm | Warp | non-inline (xterm) |
|---|---|---|---|---|
| Poster appears crisp in band | | | | n/a (chafa) |
| Char-art poster in band | n/a | n/a | n/a | |
| Scroll list — no flicker/erase | | | | |
| Select new item — poster swaps | | | | |
| Resize terminal — poster repositions | | | | |
| Open download dialog — image cleared | | | | |
| Close dialog — image returns | | | | |
| Tab to Downloads — no stray image | | | | |
| Tab back to Browse — image returns | | | | |
| Enter search — image cleared | | | | |
| Exit search — image returns | | | | |
| Poster fetch fails — placeholder box, no crash | | | | |
| Borders aligned (no drift) | | | | |

## Calibration note

If the inline image is off by a row/column on a terminal, adjust ONLY the named constants in `internal/tui/layout.go` (`bandBorder`, `bandPadV/H`, `footerRows`, `docMarginV/H`) — never reintroduce magic numbers. Record the correct values per terminal in the matrix doc.
