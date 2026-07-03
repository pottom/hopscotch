# capture_tui.tape — TUI screenshots for docs/, driven by scripts/demo/screenshots.sh
# against the synthetic fixture it builds (see screenshots.sh for what's running).
# Not meant to be run standalone — needs $REPO_ROOT/dist/hopscotch built and
# $HOPSCOTCH_CONFIG pointed at the fixture's scratch config.yaml.

Output .hopscotch-demo-tui.gif

Set Shell "bash"
Set FontFamily "Menlo"
Set FontSize 15
Set Width 1400
Set Height 780
Set Padding 20
Set WindowBar Colorful
Set BorderRadius 8

Type "dist/hopscotch status --config $HOPSCOTCH_CONFIG" Enter
Sleep 3s

Screenshot docs/tui-status.png

Type "2"
Sleep 1s
Screenshot docs/tui-rules.png

Type "3"
Sleep 2s
Screenshot docs/tui-logs.png

Type "4"
Sleep 1s
Screenshot docs/tui-settings.png

Ctrl+C
