# capture_tui.tape — TUI screenshots for docs/, driven by scripts/demo/screenshots.sh
# against the synthetic fixture it builds (see screenshots.sh for what's running).
# Not meant to be run standalone — needs $REPO_ROOT/dist/hopscotch built and
# $HOPSCOTCH_CONFIG pointed at the fixture's scratch config.yaml.

Output .hopscotch-demo-tui.gif

Set Shell "bash"
Set FontFamily "Menlo"
Set FontSize 20
Set Width 2100
Set Height 1100
Set Padding 20

Type "dist/hopscotch status --config $HOPSCOTCH_CONFIG" Enter
Sleep 3s

# Status tab: enable per-tunnel graphs (off by default) and let enough SSE
# ticks land for a real waveform, not a flat line.
Type "f"
Sleep 10s
Screenshot docs/tui-status.png

# Mirror-graph variant (download-only, "g" toggles it) — same data, alternate view.
Type "g"
Sleep 1s
Screenshot docs/tui-status-mirror.png

# Rules tab: URL tester in action, testing a CIDR-block pattern match.
Type "2"
Sleep 1s
Type "/"
Type "10.42.0.5"
Sleep 1s
Screenshot docs/tui-rules.png
Escape

# Logs tab: cycle level to WARN+ so the cache-warm retry warnings stand out.
Type "3"
Sleep 2s
Type "l"
Sleep 1s
Screenshot docs/tui-logs.png

Type "4"
Sleep 1s
Screenshot docs/tui-settings.png

Ctrl+C
