package tui

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	colorful "github.com/lucasb-eyer/go-colorful"

	"hopscotch/internal/admin"
)

// ── Palette (mirrors app.js) ──────────────────────────────────────────────────

var palette = []lipgloss.Color{
	"#38bdf8",
	"#818cf8",
	"#34d399",
	"#f59e0b",
	"#f87171",
}

const directColor = lipgloss.Color("#64748b")

var (
	colorBpsIn  = lipgloss.Color("#38bdf8")
	colorBpsOut = lipgloss.Color("#818cf8")
)

// ── Styles ────────────────────────────────────────────────────────────────────

var (
	colorConnected    = lipgloss.Color("#34d399")
	colorConnecting   = lipgloss.Color("#fbbf24")
	colorDisconnected = lipgloss.Color("#f87171")
	colorMuted        = lipgloss.Color("#475569")
	colorAccent       = lipgloss.Color("#38bdf8")
	colorText         = lipgloss.Color("#cbd5e1")
	colorBright       = lipgloss.Color("#e2e8f0")

	styleHeader = lipgloss.NewStyle().Bold(true).Foreground(colorBright)
	styleMuted  = lipgloss.NewStyle().Foreground(colorMuted)
	styleAccent = lipgloss.NewStyle().Foreground(colorAccent)
	styleText   = lipgloss.NewStyle().Foreground(colorText)

	styleConnected    = lipgloss.NewStyle().Foreground(colorConnected)
	styleConnecting   = lipgloss.NewStyle().Foreground(colorConnecting)
	styleDisconnected = lipgloss.NewStyle().Foreground(colorDisconnected)

	styleBadgeHealthy  = lipgloss.NewStyle().Foreground(colorConnected).Bold(true)
	styleBadgeDegraded = lipgloss.NewStyle().Foreground(colorConnecting).Bold(true)
	styleBadgeOffline  = lipgloss.NewStyle().Foreground(colorDisconnected).Bold(true)

	styleColName   = lipgloss.NewStyle().Foreground(colorBright).Width(26)
	styleColHost   = lipgloss.NewStyle().Foreground(colorMuted).Width(22)
	styleColPort   = lipgloss.NewStyle().Foreground(colorText).Width(7)
	styleColStatus = lipgloss.NewStyle().Width(16)
	styleColUptime = lipgloss.NewStyle().Foreground(colorText).Width(10)
	styleColRecon  = lipgloss.NewStyle().Foreground(colorMuted).Width(5)
	styleColBpsIn  = lipgloss.NewStyle().Foreground(colorBpsIn).Width(15)
	styleColBpsOut = lipgloss.NewStyle().Foreground(colorBpsOut).Width(15)
	styleColConn   = lipgloss.NewStyle().Foreground(colorText).Width(8)

	styleTabActive   = lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	styleTabInactive = lipgloss.NewStyle().Foreground(colorMuted)
)

// ── Tabs ──────────────────────────────────────────────────────────────────────

const (
	tabStatus = 0
	tabLogs   = 1
	numTabs   = 2
)

const headerHeight = 5 // blank · title+tabs · stats · label · separator

func (m Model) headerHeight() int { return headerHeight }

const footerHeight = 2 // separator newline + hints+ports line

// ── Traffic data ──────────────────────────────────────────────────────────────

const windowSize = 300
const maxLogLines = 300

// brailleBit returns the bit value for a dot at (dotCol 0-1, dotRow 0-3).
// Braille layout: col0={1,2,4,64}, col1={8,16,32,128}
var brailleBit = [2][4]uint8{
	{1, 2, 4, 64},
	{8, 16, 32, 128},
}

const graphRows = 4 // terminal rows per tunnel graph

type trafficWindow struct {
	dataIn      []float64
	dataOut     []float64
	bpsIn       uint64
	bpsOut      uint64
	active      int64
	reconnectIn *int
}

func (w *trafficWindow) push(bpsIn, bpsOut uint64) {
	w.bpsIn = bpsIn
	w.bpsOut = bpsOut
	w.dataIn = append(w.dataIn, float64(bpsIn))
	w.dataOut = append(w.dataOut, float64(bpsOut))
	if len(w.dataIn) > windowSize {
		w.dataIn = w.dataIn[1:]
		w.dataOut = w.dataOut[1:]
	}
}

func padData(data []float64, width int) []float64 {
	out := make([]float64, width)
	if n := len(data); n >= width {
		copy(out, data[n-width:])
	} else if n > 0 {
		copy(out[width-n:], data)
	}
	return out
}

func onesCount8(b uint8) int {
	count := 0
	for b != 0 {
		count += int(b & 1)
		b >>= 1
	}
	return count
}

// renderGraph draws a braille line graph for one tunnel.
// When mirror=true: download fills upward from the centre, upload downward.
// When mirror=false: only download, filling upward from the bottom.
// Returns `rows` strings (top to bottom).
func renderGraph(inData, outData []float64, colorIn, colorOut lipgloss.Color, rows, width int, mirror bool) []string {
	totalBrailleRows := rows * 4
	dataWidth := width * 2 // 2 data points per terminal char

	padIn := padData(inData, dataWidth)
	padOut := padData(outData, dataWidth)

	maxVal := 0.0
	for _, v := range padIn {
		if v > maxVal {
			maxVal = v
		}
	}
	for _, v := range padOut {
		if v > maxVal {
			maxVal = v
		}
	}

	type cell struct {
		bits    uint8
		inBits  uint8
		outBits uint8
	}
	grid := make([][]cell, rows)
	for r := range grid {
		grid[r] = make([]cell, width)
	}

	center := totalBrailleRows / 2

	setDot := func(bRow, dotCol, charCol int, isIn bool) {
		if bRow < 0 || bRow >= totalBrailleRows {
			return
		}
		charRow := bRow / 4
		dotRow := bRow % 4
		bit := brailleBit[dotCol][dotRow]
		grid[charRow][charCol].bits |= bit
		if isIn {
			grid[charRow][charCol].inBits |= bit
		} else {
			grid[charRow][charCol].outBits |= bit
		}
	}

	for dc := 0; dc < dataWidth; dc++ {
		charCol := dc / 2
		dotCol := dc % 2

		inNorm := 0.0
		if maxVal > 0 {
			inNorm = padIn[dc] / maxVal
		}
		outNorm := 0.0
		if maxVal > 0 {
			outNorm = padOut[dc] / maxVal
		}

		if mirror {
			// Download: fill upward from centre to peak
			inPeak := center - 1 - int(math.Round(inNorm*float64(center-1)))
			for bRow := inPeak; bRow < center; bRow++ {
				setDot(bRow, dotCol, charCol, true)
			}

			// Upload: fill downward from centre to peak
			outPeak := center + int(math.Round(outNorm*float64(center-1)))
			for bRow := center; bRow <= outPeak; bRow++ {
				setDot(bRow, dotCol, charCol, false)
			}
		} else {
			// Single channel: fill upward from bottom to peak
			inPeak := totalBrailleRows - 1 - int(math.Round(inNorm*float64(totalBrailleRows-1)))
			for bRow := inPeak; bRow < totalBrailleRows; bRow++ {
				setDot(bRow, dotCol, charCol, true)
			}
		}
	}

	result := make([]string, rows)
	for r := range result {
		var sb strings.Builder
		for c := 0; c < width; c++ {
			cl := grid[r][c]
			timeT := float64(c) / float64(max(width-1, 1))

			var col lipgloss.Color
			switch {
			case cl.bits == 0:
				col = colorMuted
			case cl.outBits == 0:
				col = blendColor(colorMuted, colorIn, timeT)
			case cl.inBits == 0:
				col = blendColor(colorMuted, colorOut, timeT)
			default:
				// Boundary char: blend by bit count ratio
				t := float64(onesCount8(cl.inBits)) / float64(onesCount8(cl.inBits)+onesCount8(cl.outBits))
				mid := blendColor(colorOut, colorIn, t)
				col = blendColor(colorMuted, mid, timeT)
			}

			char := rune(0x2800 + int(cl.bits))
			sb.WriteString(lipgloss.NewStyle().Foreground(col).Render(string(char)))
		}
		result[r] = sb.String()
	}
	return result
}

// blendColor interpolates between two hex lipgloss colors in Lab space.
func blendColor(from, to lipgloss.Color, t float64) lipgloss.Color {
	c1, err1 := colorful.Hex(string(from))
	c2, err2 := colorful.Hex(string(to))
	if err1 != nil || err2 != nil {
		return to
	}
	return lipgloss.Color(c1.BlendLab(c2, t).Hex())
}


// ── SSE types ─────────────────────────────────────────────────────────────────

type sseTrafficEntry struct {
	BpsIn       uint64 `json:"bps_in"`
	BpsOut      uint64 `json:"bps_out"`
	Active      int64  `json:"active"`
	ReconnectIn *int   `json:"reconnect_in,omitempty"`
}

type ssePayload struct {
	Tunnels map[string]sseTrafficEntry `json:"tunnels"`
	Direct  sseTrafficEntry            `json:"direct"`
}

// ── Messages ──────────────────────────────────────────────────────────────────

type statusMsg  admin.StatusResponse
type sseMsg     ssePayload
type logLineMsg string
type errMsg     error
type tickMsg    time.Time

// ── Model ─────────────────────────────────────────────────────────────────────

type Model struct {
	status  admin.StatusResponse
	traffic map[string]*trafficWindow
	err     error

	adminURL string
	sseURL   string
	logURL   string
	sseCh    chan ssePayload
	logCh    chan string
	done     chan struct{}

	activeTab  int
	logLines   []string
	logVP      viewport.Model
	logVPReady bool

	tick    int
	width   int
	height  int
	vp      viewport.Model
	vpReady bool
	compact     bool
	mirrorGraph bool
	ready       bool
}

func New(adminURL string) Model {
	base := strings.TrimSuffix(adminURL, "/status")
	sseCh := make(chan ssePayload, 8)
	logCh := make(chan string, 64)
	done := make(chan struct{})
	return Model{
		adminURL: adminURL,
		sseURL:   base + "/traffic/stream",
		logURL:   base + "/logs/stream",
		sseCh:    sseCh,
		logCh:    logCh,
		done:     done,
		traffic:     make(map[string]*trafficWindow),
		mirrorGraph: true,
		width:       80,
		height:      24,
	}
}

func (m Model) Init() tea.Cmd {
	go runSSE(m.sseURL, m.sseCh, m.done)
	go runLogSSE(m.logURL, m.logCh, m.done)
	return tea.Batch(fetchStatus(m.adminURL), tickEvery(), waitForSSE(m.sseCh), waitForLog(m.logCh))
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "Q", "ctrl+c", "esc":
			close(m.done)
			return m, tea.Quit

		case "tab":
			m.activeTab = (m.activeTab + 1) % numTabs
			m = m.resizeViewports()
			return m, nil

		case "s", "S":
			m.activeTab = tabStatus
			m = m.resizeViewports()
			return m, nil

		case "l", "L":
			m.activeTab = tabLogs
			m = m.resizeViewports()
			return m, nil

		case "c", "C":
			if m.activeTab == tabStatus {
				m.compact = !m.compact
				if m.vpReady {
					m.vp.SetContent(m.buildStatusContent())
				}
			}
			return m, nil

		case "g", "G":
			if m.activeTab == tabStatus {
				m.mirrorGraph = !m.mirrorGraph
				if m.vpReady {
					m.vp.SetContent(m.buildStatusContent())
				}
			}
			return m, nil

		default:
			if m.activeTab == tabStatus && m.vpReady {
				m.vp, cmd = m.vp.Update(msg)
			} else if m.activeTab == tabLogs && m.logVPReady {
				m.logVP, cmd = m.logVP.Update(msg)
			}
			return m, cmd
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m = m.resizeViewports()

	case statusMsg:
		m.status = admin.StatusResponse(msg)
		m.err = nil
		m.ready = true
		for name := range m.status.Tunnels {
			if m.traffic[name] == nil {
				m.traffic[name] = &trafficWindow{}
			}
		}
		if m.traffic["direct"] == nil {
			m.traffic["direct"] = &trafficWindow{}
		}
		if m.vpReady {
			m.vp.SetContent(m.buildStatusContent())
		}

	case sseMsg:
		for name, t := range msg.Tunnels {
			if m.traffic[name] == nil {
				m.traffic[name] = &trafficWindow{}
			}
			m.traffic[name].push(t.BpsIn, t.BpsOut)
			m.traffic[name].active = t.Active
			m.traffic[name].reconnectIn = t.ReconnectIn
		}
		if m.traffic["direct"] == nil {
			m.traffic["direct"] = &trafficWindow{}
		}
		m.traffic["direct"].push(msg.Direct.BpsIn, msg.Direct.BpsOut)
		m.traffic["direct"].active = msg.Direct.Active
		if m.vpReady {
			m.vp.SetContent(m.buildStatusContent())
		}
		return m, waitForSSE(m.sseCh)

	case logLineMsg:
		m.logLines = append(m.logLines, string(msg))
		if len(m.logLines) > maxLogLines {
			m.logLines = m.logLines[1:]
		}
		if m.logVPReady {
			atBottom := m.logVP.AtBottom()
			m.logVP.SetContent("  " + strings.Join(m.logLines, "\n  "))
			if atBottom {
				m.logVP.GotoBottom()
			}
		}
		return m, waitForLog(m.logCh)

	case errMsg:
		m.err = msg
		m.ready = true

	case tickMsg:
		m.tick++
		if m.vpReady {
			m.vp.SetContent(m.buildStatusContent())
		}
		return m, tea.Batch(fetchStatus(m.adminURL), tickEvery())
	}

	return m, cmd
}

// resizeViewports recalculates both viewports for the current terminal size
// and active tab. Called on WindowSizeMsg and when switching tabs.
func (m Model) resizeViewports() Model {
	statusVpH := m.height - headerHeight - footerHeight
	logsVpH   := m.height - headerHeight - footerHeight
	if statusVpH < 1 {
		statusVpH = 1
	}
	if logsVpH < 1 {
		logsVpH = 1
	}

	if !m.vpReady {
		m.vp = viewport.New(m.width, statusVpH)
		m.vpReady = true
	} else {
		m.vp.Width = m.width
		m.vp.Height = statusVpH
	}
	m.vp.SetContent(m.buildStatusContent())

	if !m.logVPReady {
		m.logVP = viewport.New(m.width, logsVpH)
		m.logVPReady = true
	} else {
		m.logVP.Width = m.width
		m.logVP.Height = logsVpH
	}
	if len(m.logLines) > 0 {
		atBottom := m.logVP.AtBottom()
		m.logVP.SetContent("  " + strings.Join(m.logLines, "\n  "))
		if atBottom {
			m.logVP.GotoBottom()
		}
	}

	return m
}

func (m Model) View() string {
	if !m.ready {
		return styleMuted.Render("\n  connecting…") + "\n"
	}
	if m.err != nil {
		return styleDisconnected.Render("\n  hopscotch is not running") + "\n"
	}

	vp := m.vp.View()
	if m.activeTab == tabLogs {
		vp = m.logVP.View()
	}
	return m.renderHeader() + vp + m.renderFooter()
}

// ── Header renderers ──────────────────────────────────────────────────────────

func (m Model) renderTitleLine() string {
	left := fmt.Sprintf("  %s  %s  %s  %s",
		styleHeader.Render("hopscotch "+m.status.Version),
		renderBadge(m.status.Status),
		styleMuted.Render(fmt.Sprintf("PID %d", m.status.PID)),
		styleMuted.Render("up "+m.status.Uptime),
	)
	right := m.renderTabBar() + "  "
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 2 {
		gap = 2
	}
	return "\n" + left + strings.Repeat(" ", gap) + right + "\n"
}

func (m Model) renderStatsLine() string {
	totalIn, totalOut, totalActive := m.totalStats()
	return fmt.Sprintf("  %s  %s  %s\n",
		lipgloss.NewStyle().Foreground(colorBpsIn).Render(fmt.Sprintf("↓ %-12s", fmtBytes(totalIn))),
		lipgloss.NewStyle().Foreground(colorBpsOut).Render(fmt.Sprintf("↑ %-12s", fmtBytes(totalOut))),
		styleText.Render(fmt.Sprintf("%d conn total", totalActive)),
	)
}

func (m Model) renderTabBar() string {
	tabs := []struct {
		name string
		idx  int
	}{
		{"Status", tabStatus},
		{"Logs", tabLogs},
	}
	var parts []string
	for _, t := range tabs {
		if t.idx == m.activeTab {
			parts = append(parts, styleTabActive.Render(t.name))
		} else {
			parts = append(parts, styleTabInactive.Render(t.name))
		}
	}
	return strings.Join(parts, styleMuted.Render("  ·  "))
}

// renderHeader renders the shared 6-line header for all tabs.
// Line 5 is tab-specific: column labels for Status, a blank line for Logs.
func (m Model) renderHeader() string {
	var b strings.Builder
	b.WriteString(m.renderTitleLine())
	b.WriteString(m.renderStatsLine())

	if m.activeTab == tabStatus {
		hdr := func(s lipgloss.Style, label string) string {
			return s.Foreground(colorMuted).Render(label)
		}
		rW := m.width - fixedColsWidth - 2
		reasonHdr := ""
		if rW >= 8 {
			reasonHdr = styleMuted.Render("REASON")
		}
		fmt.Fprintf(&b, "  %s%s%s%s%s%s%s%s%s%s\n",
			hdr(styleColName, "TUNNEL"),
			hdr(styleColHost, "HOST"),
			hdr(styleColPort, "PORT"),
			hdr(styleColStatus, "STATUS"),
			hdr(styleColUptime, "UPTIME"),
			hdr(styleColRecon, "RC"),
			hdr(styleColBpsIn, "↓"),
			hdr(styleColBpsOut, "↑"),
			hdr(styleColConn, "CONN"),
			reasonHdr,
		)
	} else {
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "  %s\n", styleMuted.Render(strings.Repeat("─", m.width-4)))

	return b.String()
}

// renderFooter returns a single-line bar: hints on the left, ports on the right.
func (m Model) renderFooter() string {
	hints := "q quit  tab/s/l switch  ↑↓/jk scroll"
	if m.activeTab == tabStatus {
		if m.compact {
			hints += "  c expand"
		} else {
			hints += "  c compact"
		}
		if m.mirrorGraph {
			hints += "  g single"
		} else {
			hints += "  g mirror"
		}
	}

	activeVP := m.vp
	if m.activeTab == tabLogs {
		activeVP = m.logVP
	}
	if !activeVP.AtBottom() {
		hints += "  ↓"
	}

	ports := fmt.Sprintf("PROXY :%d  ADMIN :%d", m.status.ProxyPort, m.status.AdminPort)
	left := styleMuted.Render(hints)
	right := styleMuted.Render(ports)

	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right) - 4
	if gap < 2 {
		gap = 2
	}

	return "\n  " + left + strings.Repeat(" ", gap) + right + "\n"
}

// buildStatusContent renders the scrollable tunnel list for the status viewport.
// fixedColsWidth is the sum of all fixed-width column chars (indent + all styled cols).
const fixedColsWidth = 2 + 26 + 22 + 7 + 16 + 10 + 5 + 15 + 15 + 8 // = 126

func (m Model) buildStatusContent() string {
	sparkW := m.width - 4
	if sparkW < 10 {
		sparkW = 10
	}

	// Remaining space after fixed columns for the reason field.
	reasonW := m.width - fixedColsWidth - 2
	if reasonW < 8 {
		reasonW = 0
	}

	var b strings.Builder

	names := make([]string, 0, len(m.status.Tunnels))
	for name := range m.status.Tunnels {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		t := m.status.Tunnels[name]
		w := m.traffic[name]

		uptime := "—"
		if t.UptimeSeconds > 0 {
			uptime = fmtDuration(time.Duration(t.UptimeSeconds) * time.Second)
		}

		var reconnectIn *int
		var bpsIn, bpsOut uint64
		var active int64
		if w != nil {
			reconnectIn = w.reconnectIn
			bpsIn = w.bpsIn
			bpsOut = w.bpsOut
			active = w.active
		}

		reasonStr := ""
		if reasonW > 0 {
			reason := "—"
			var reasonStyle lipgloss.Style
			if t.LastError != "" && t.Status != "connected" {
				reason = t.LastError
				reasonStyle = lipgloss.NewStyle().Foreground(colorDisconnected)
			} else {
				reasonStyle = styleMuted
			}
			// fixedColsWidth+4 = 2 (row indent) + columns + 2 (separator before reason)
			reasonStr = renderReason(reason, reasonStyle, reasonW, fixedColsWidth+2)
		}
		fmt.Fprintf(&b, "  %s%s%s%s%s%s%s%s%s%s\n",
			styleColName.Render(name),
			styleColHost.Render(t.Host),
			styleColPort.Render(fmt.Sprintf("%d", t.LocalPort)),
			styleColStatus.Render(renderStatus(t.Status, m.tick, reconnectIn, t.KeepaliveFailures)),
			styleColUptime.Render(uptime),
			styleColRecon.Render(fmt.Sprintf("%d", t.ReconnectCount)),
			styleColBpsIn.Render("↓ "+fmtBytes(bpsIn)),
			styleColBpsOut.Render("↑ "+fmtBytes(bpsOut)),
			styleColConn.Render(fmtActive(active)),
			reasonStr,
		)

		if !m.compact && w != nil {
			for _, line := range renderGraph(w.dataIn, w.dataOut, colorBpsIn, colorBpsOut, graphRows, sparkW, m.mirrorGraph) {
				fmt.Fprintf(&b, "  %s\n", line)
			}
		}
	}

	// direct
	dw := m.traffic["direct"]
	var dBpsIn, dBpsOut uint64
	var dActive int64
	if dw != nil {
		dBpsIn = dw.bpsIn
		dBpsOut = dw.bpsOut
		dActive = dw.active
	}
	fmt.Fprintf(&b, "  %s%s%s%s%s%s%s%s\n",
		styleColName.Foreground(colorMuted).Render("direct"),
		styleColPort.Render(""),
		styleColStatus.Render(""),
		styleColUptime.Render(""),
		styleColRecon.Render(""),
		styleColBpsIn.Render("↓ "+fmtBytes(dBpsIn)),
		styleColBpsOut.Render("↑ "+fmtBytes(dBpsOut)),
		styleColConn.Render(fmtActive(dActive)),
	)
	if !m.compact && dw != nil {
		for _, line := range renderGraph(dw.dataIn, dw.dataOut, colorBpsIn, colorBpsOut, graphRows, sparkW, m.mirrorGraph) {
			fmt.Fprintf(&b, "  %s\n", line)
		}
	}

	return b.String()
}

// totalStats returns the sum of bps and active connections across all tunnels and direct.
func (m Model) totalStats() (bpsIn, bpsOut uint64, active int64) {
	for _, w := range m.traffic {
		if w == nil {
			continue
		}
		bpsIn += w.bpsIn
		bpsOut += w.bpsOut
		active += w.active
	}
	return
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func fmtActive(n int64) string {
	return fmt.Sprintf("%d conn", n)
}

// wrapAt breaks s into lines of at most width runes, splitting at spaces where possible.
func wrapAt(s string, width int) []string {
	if width <= 0 || len(s) <= width {
		return []string{s}
	}
	var lines []string
	for len(s) > width {
		cut := width
		if i := strings.LastIndex(s[:cut], " "); i > 0 {
			cut = i
		}
		lines = append(lines, s[:cut])
		s = strings.TrimLeft(s[cut:], " ")
	}
	if s != "" {
		lines = append(lines, s)
	}
	return lines
}

// renderReason returns the inline reason string (with possible embedded newlines for wrapping).
// indent is the number of spaces to prepend on continuation lines.
func renderReason(reason string, style lipgloss.Style, reasonW, indent int) string {
	if reasonW <= 0 {
		return ""
	}
	lines := wrapAt(reason, reasonW)
	indentStr := strings.Repeat(" ", indent)
	var parts []string
	for i, l := range lines {
		if i == 0 {
			parts = append(parts, "  "+style.Render(l))
		} else {
			parts = append(parts, indentStr+style.Render(l))
		}
	}
	return strings.Join(parts, "\n")
}

func renderBadge(status string) string {
	switch status {
	case "healthy":
		return styleBadgeHealthy.Render("✓ healthy")
	case "degraded":
		return styleBadgeDegraded.Render("! degraded")
	default:
		return styleBadgeOffline.Render("✗ " + status)
	}
}

func renderStatus(status string, tick int, reconnectIn *int, keepaliveFails int) string {
	switch status {
	case "connected":
		if keepaliveFails > 0 {
			return styleConnected.Render(fmt.Sprintf("● connected ⚠%d", keepaliveFails))
		}
		return styleConnected.Render("● connected")
	case "connecting":
		dot := "●"
		if tick%2 == 0 {
			dot = "○"
		}
		if reconnectIn != nil && *reconnectIn >= 0 {
			return styleConnecting.Render(fmt.Sprintf("%s %ds", dot, *reconnectIn))
		}
		return styleConnecting.Render(dot + " connecting")
	case "disconnected":
		return styleDisconnected.Render("○ disconnected")
	default:
		return styleMuted.Render("? " + status)
	}
}

func fmtBytes(n uint64) string {
	switch {
	case n == 0:
		return "0 B/s"
	case n < 1024:
		return fmt.Sprintf("%d B/s", n)
	case n < 1048576:
		return fmt.Sprintf("%.1f KB/s", float64(n)/1024)
	default:
		return fmt.Sprintf("%.2f MB/s", float64(n)/1048576)
	}
}

func fmtDuration(d time.Duration) string {
	d = d.Round(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh%dm", h, m)
	}
	if m > 0 {
		return fmt.Sprintf("%dm%ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

// ── Commands ──────────────────────────────────────────────────────────────────

func tickEvery() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func fetchStatus(url string) tea.Cmd {
	return func() tea.Msg {
		resp, err := http.Get(url) //nolint:noctx
		if err != nil {
			return errMsg(err)
		}
		defer resp.Body.Close()
		var st admin.StatusResponse
		if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
			return errMsg(err)
		}
		return statusMsg(st)
	}
}

func waitForSSE(ch <-chan ssePayload) tea.Cmd {
	return func() tea.Msg {
		return sseMsg(<-ch)
	}
}

func waitForLog(ch <-chan string) tea.Cmd {
	return func() tea.Msg {
		return logLineMsg(<-ch)
	}
}

func runSSE(url string, ch chan<- ssePayload, done <-chan struct{}) {
	for {
		select {
		case <-done:
			return
		default:
		}

		resp, err := http.Get(url) //nolint:noctx
		if err != nil {
			select {
			case <-done:
				return
			case <-time.After(3 * time.Second):
				continue
			}
		}

		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			var p ssePayload
			if err := json.Unmarshal([]byte(line[6:]), &p); err != nil {
				continue
			}
			select {
			case ch <- p:
			case <-done:
				resp.Body.Close()
				return
			}
		}
		resp.Body.Close()

		select {
		case <-done:
			return
		case <-time.After(time.Second):
		}
	}
}

func runLogSSE(url string, ch chan<- string, done <-chan struct{}) {
	for {
		select {
		case <-done:
			return
		default:
		}

		resp, err := http.Get(url) //nolint:noctx
		if err != nil {
			select {
			case <-done:
				return
			case <-time.After(3 * time.Second):
				continue
			}
		}

		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 64*1024), 64*1024)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			select {
			case ch <- line[6:]:
			case <-done:
				resp.Body.Close()
				return
			}
		}
		resp.Body.Close()

		select {
		case <-done:
			return
		case <-time.After(time.Second):
		}
	}
}
