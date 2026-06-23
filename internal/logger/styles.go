package logger

import (
	"github.com/charmbracelet/lipgloss"
	clog "github.com/charmbracelet/log"
)

// Log level colours — single source of truth for both the terminal (via
// charmbracelet/log styles) and the web UI (via ansi_up converting the same
// ANSI codes that the logger emits).
const (
	ColorDebug = lipgloss.Color("63")  // #5f5fff blue-purple
	ColorInfo  = lipgloss.Color("86")  // #5fffd7 cyan-green
	ColorWarn  = lipgloss.Color("192") // #d7ff87 yellow-green
	ColorError = lipgloss.Color("204") // #ff5f87 pink-red
	ColorFatal = lipgloss.Color("134") // #af5fd7 purple
)

func buildStyles() *clog.Styles {
	s := clog.DefaultStyles()
	s.Levels[clog.DebugLevel] = lipgloss.NewStyle().SetString("DEBU").Bold(true).MaxWidth(4).Foreground(ColorDebug)
	s.Levels[clog.InfoLevel]  = lipgloss.NewStyle().SetString("INFO").Bold(true).MaxWidth(4).Foreground(ColorInfo)
	s.Levels[clog.WarnLevel]  = lipgloss.NewStyle().SetString("WARN").Bold(true).MaxWidth(4).Foreground(ColorWarn)
	s.Levels[clog.ErrorLevel] = lipgloss.NewStyle().SetString("ERRO").Bold(true).MaxWidth(4).Foreground(ColorError)
	s.Levels[clog.FatalLevel] = lipgloss.NewStyle().SetString("FATA").Bold(true).MaxWidth(4).Foreground(ColorFatal)
	return s
}
