package tui

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/cookiejar"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	colorful "github.com/lucasb-eyer/go-colorful"

	"github.com/pottom/hopscotch/internal/admin"
	"github.com/pottom/hopscotch/internal/config"
	"github.com/pottom/hopscotch/internal/msgs"
	"github.com/pottom/hopscotch/internal/proxy"
)

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

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
	colorVPN          = lipgloss.Color("#2dd4bf")
	colorDirect       = lipgloss.Color("#a78bfa")
	colorText         = lipgloss.Color("#cbd5e1")
	colorBright       = lipgloss.Color("#e2e8f0")

	styleHeader = lipgloss.NewStyle().Bold(true).Foreground(colorBright)
	styleMuted  = lipgloss.NewStyle().Foreground(colorMuted)
	styleText   = lipgloss.NewStyle().Foreground(colorText)

	styleConnected    = lipgloss.NewStyle().Foreground(colorConnected)
	styleConnecting   = lipgloss.NewStyle().Foreground(colorConnecting)
	styleDisconnected = lipgloss.NewStyle().Foreground(colorDisconnected)

	styleBadgeHealthy  = lipgloss.NewStyle().Foreground(colorConnected).Bold(true)
	styleBadgeDegraded = lipgloss.NewStyle().Foreground(colorConnecting).Bold(true)
	styleBadgeOffline  = lipgloss.NewStyle().Foreground(colorDisconnected).Bold(true)

	styleTabActive   = lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	styleTabInactive = lipgloss.NewStyle().Foreground(colorMuted)

	styleRouteNum     = lipgloss.NewStyle().Foreground(colorMuted).Width(4)
	styleRoutePattern = lipgloss.NewStyle().Foreground(colorBright).Width(32)

	styleEditNew     = lipgloss.NewStyle().Foreground(colorConnected)
	styleEditDeleted = lipgloss.NewStyle().Foreground(colorDisconnected)
)

// ── Tabs ──────────────────────────────────────────────────────────────────────

const (
	tabStatus = 0
	tabRoutes = 1
	tabLogs   = 2
	numTabs   = 3
)

// headerLines returns the number of header lines for the current terminal width.
// Normally 3 (title+tabs · stats · blank); becomes 4 when the title meta wraps.
func (m Model) headerLines() int {
	if m.width > 0 && lipgloss.Width(m.titleLeft())+lipgloss.Width(m.renderTabBar())+6 > m.width {
		return 4
	}
	return 3
}

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

type sseVPNEntry struct {
	ReconnectIn *int `json:"reconnect_in,omitempty"`
}

type ssePayload struct {
	Tunnels map[string]sseTrafficEntry `json:"tunnels"`
	VPNs    map[string]sseVPNEntry    `json:"vpns,omitempty"`
	Direct  sseTrafficEntry            `json:"direct"`
}

// ── Messages ──────────────────────────────────────────────────────────────────

type statusMsg    admin.StatusResponse
type sseMsg       ssePayload
type logLineMsg   string
type errMsg       error
type tickMsg      time.Time
type rulesSavedMsg   struct{}
type rulesSaveErrMsg struct{ err error }

// editRule wraps a route with diff metadata (mirrors web UI soft-delete model).
type editRule struct {
	admin.RouteJSON
	origPattern string // for change detection
	origTunnel  string
	origVia     string
	isNew       bool
	isDeleted   bool
	isModified  bool
	validErr    string
}

func (r *editRule) recomputeModified() {
	r.isModified = r.Pattern != r.origPattern || r.Tunnel != r.origTunnel || r.Via != r.origVia
}

func (r *editRule) validate() {
	if r.Pattern == "" {
		r.validErr = "pattern is required"
		return
	}
	if err := config.ValidatePattern(r.Pattern); err != nil {
		r.validErr = err.Error()
		return
	}
	r.validErr = ""
}

// ── Model ─────────────────────────────────────────────────────────────────────

type Model struct {
	status  admin.StatusResponse
	traffic map[string]*trafficWindow
	err     error

	adminURL   string
	sseURL     string
	logURL     string
	sseCh      chan ssePayload
	logCh      chan string
	done       chan struct{}
	httpClient *http.Client

	activeTab    int
	logLines     []string
	logLevel     int // 0=DEBUG 1=INFO 2=WARN 3=ERROR
	logVP        viewport.Model
	logVPReady   bool
	routeVP      viewport.Model
	routeVPReady bool
	routeInput   textinput.Model
	routeFocused bool

	// edit mode (Patterns tab)
	editMode      bool
	editRules     []editRule
	editCursor    int
	editPatInput  textinput.Model
	editPatFocused bool
	editError     string
	editSaving    bool

	tick    int
	width   int
	height  int
	vp      viewport.Model
	vpReady bool
	compact     bool
	mirrorGraph bool
	ready       bool
}

// New creates a TUI Model using the provided pre-authenticated http.Client.
// The caller (cmd/status.go) is responsible for resolving credentials and
// performing login before calling New.
func New(adminURL string, client *http.Client) Model {
	base := strings.TrimSuffix(adminURL, "/status")
	sseCh := make(chan ssePayload, 8)
	logCh := make(chan string, 64)
	done := make(chan struct{})

	if client == nil {
		jar, _ := cookiejar.New(nil)
		client = &http.Client{Jar: jar}
	}

	ti := textinput.New()
	ti.Placeholder = "hostname or URL — Ctrl+N to clear"
	ti.CharLimit = 256
	ti.Width = 60

	ep := textinput.New()
	ep.Placeholder = "*.example.com or 10.0.0.0/8"
	ep.CharLimit = 256
	ep.Width = 32

	return Model{
		adminURL:    adminURL,
		sseURL:      base + "/traffic/stream",
		logURL:      base + "/logs/stream",
		sseCh:       sseCh,
		logCh:       logCh,
		done:        done,
		httpClient:  client,
		traffic:     make(map[string]*trafficWindow),
		compact:     true,
		mirrorGraph: true,
		width:       80,
		height:      24,
		routeInput:   ti,
		editPatInput: ep,
		logLevel:     1, // default: INFO+
	}
}

func (m Model) Init() tea.Cmd {
	go runSSE(m.sseURL, m.httpClient, m.sseCh, m.done)
	go runLogSSE(m.logURL, m.httpClient, m.logCh, m.done)
	return tea.Batch(fetchStatus(m.adminURL, m.httpClient), tickEvery(), waitForSSE(m.sseCh), waitForLog(m.logCh))
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {

	case tea.KeyMsg:
		// Pattern edit input (inside edit mode) — most keys go to textinput.
		if m.editPatFocused {
			switch msg.String() {
			case "esc":
				m.editPatFocused = false
				m.editPatInput.Blur()
				if m.routeVPReady {
					m.routeVP.SetContent(m.buildRoutesEditContent())
				}
				return m, nil
			case "enter":
				if m.editCursor < len(m.editRules) {
					r := &m.editRules[m.editCursor]
					r.Pattern = m.editPatInput.Value()
					r.recomputeModified()
					r.validate()
				}
				m.editPatFocused = false
				m.editPatInput.Blur()
				if m.routeVPReady {
					m.routeVP.SetContent(m.buildRoutesEditContent())
				}
				return m, nil
			default:
				m.editPatInput, cmd = m.editPatInput.Update(msg)
				// Live validation while typing
				if m.editCursor < len(m.editRules) {
					r := &m.editRules[m.editCursor]
					r.Pattern = m.editPatInput.Value()
					r.validate()
				}
				if m.routeVPReady {
					m.routeVP.SetContent(m.buildRoutesEditContent())
				}
				return m, cmd
			}
		}

		// URL tester input.
		if m.routeFocused {
			switch msg.String() {
			case "esc", "enter":
				m.routeFocused = false
				m.routeInput.Blur()
			default:
				m.routeInput, cmd = m.routeInput.Update(msg)
				if m.routeVPReady {
					m.routeVP.SetContent(m.buildRoutesContent())
				}
				return m, cmd
			}
			return m, nil
		}

		// Edit mode row-selection keys.
		if m.editMode {
			switch msg.String() {
			case "q", "Q", "ctrl+c":
				close(m.done)
				return m, tea.Quit
			case "esc":
				m.editMode = false
				m.editError = ""
				if m.routeVPReady {
					m.routeVP.SetContent(m.buildRoutesContent())
				}
				return m, nil
			case "ctrl+s":
				if !m.editSaving {
					m.editSaving = true
					m.editError = ""
					if m.routeVPReady {
						m.routeVP.SetContent(m.buildRoutesEditContent())
					}
					return m, m.saveRulesCmd()
				}
				return m, nil
			case "up", "k":
				if m.editCursor > 0 {
					m.editCursor--
					if m.routeVPReady {
						m.routeVP.SetContent(m.buildRoutesEditContent())
					}
				}
				return m, nil
			case "down", "j":
				if m.editCursor < len(m.editRules)-1 {
					m.editCursor++
					if m.routeVPReady {
						m.routeVP.SetContent(m.buildRoutesEditContent())
					}
				}
				return m, nil
			case "shift+up":
				if m.editCursor > 0 {
					m.editRules[m.editCursor-1], m.editRules[m.editCursor] = m.editRules[m.editCursor], m.editRules[m.editCursor-1]
					m.editCursor--
					if m.routeVPReady {
						m.routeVP.SetContent(m.buildRoutesEditContent())
					}
				}
				return m, nil
			case "shift+down":
				if m.editCursor < len(m.editRules)-1 {
					m.editRules[m.editCursor], m.editRules[m.editCursor+1] = m.editRules[m.editCursor+1], m.editRules[m.editCursor]
					m.editCursor++
					if m.routeVPReady {
						m.routeVP.SetContent(m.buildRoutesEditContent())
					}
				}
				return m, nil
			case "d", "D":
				if m.editCursor < len(m.editRules) {
					r := &m.editRules[m.editCursor]
					if r.isNew {
						// New row: remove immediately (no history to preserve)
						m.editRules = append(m.editRules[:m.editCursor], m.editRules[m.editCursor+1:]...)
						if m.editCursor >= len(m.editRules) && m.editCursor > 0 {
							m.editCursor--
						}
					} else {
						r.isDeleted = !r.isDeleted // toggle soft-delete
					}
					if m.routeVPReady {
						m.routeVP.SetContent(m.buildRoutesEditContent())
					}
				}
				return m, nil
			case "a": // append: insert new row BELOW cursor
				m.editInsertRule(m.editCursor + 1)
				return m, textinput.Blink
			case "i": // insert: insert new row ABOVE cursor
				m.editInsertRule(m.editCursor)
				return m, textinput.Blink
			case "e", "E", "enter":
				if m.editCursor < len(m.editRules) && !m.editRules[m.editCursor].isDeleted {
					m.editPatInput.SetValue(m.editRules[m.editCursor].Pattern)
					m.editPatFocused = true
					m.editPatInput.Focus()
					if m.routeVPReady {
						m.routeVP.SetContent(m.buildRoutesEditContent())
					}
					return m, textinput.Blink
				}
				return m, nil
			case "v", "V":
				m.editCycleVia()
				if m.routeVPReady {
					m.routeVP.SetContent(m.buildRoutesEditContent())
				}
				return m, nil
			}
			return m, nil
		}

		// Ctrl+N: clear URL tester on Patterns tab.
		if msg.String() == "ctrl+n" && m.activeTab == tabRoutes {
			m.routeInput.SetValue("")
			m.routeInput.Blur()
			m.routeFocused = false
			if m.routeVPReady {
				m.routeVP.SetContent(m.buildRoutesContent())
			}
			return m, nil
		}

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

		case "p", "P":
			m.activeTab = tabRoutes
			m = m.resizeViewports()
			return m, nil

		case "e", "E":
			if m.activeTab == tabRoutes {
				m.editMode = true
				m.editError = ""
				m.editSaving = false
				m.editCursor = 0
				m.editRules = make([]editRule, len(m.status.Routes))
				for i, r := range m.status.Routes {
					m.editRules[i] = editRule{
						RouteJSON:   r,
						origPattern: r.Pattern,
						origTunnel:  r.Tunnel,
						origVia:     r.Via,
					}
				}
				if m.routeVPReady {
					m.routeVP.SetContent(m.buildRoutesEditContent())
				}
				return m, nil
			}
			return m, nil

		case "/":
			if m.activeTab == tabRoutes {
				m.routeFocused = true
				m.routeInput.Focus()
				return m, textinput.Blink
			}
			return m, nil

		case "f", "F":
			if m.activeTab == tabStatus {
				m.compact = !m.compact
				if m.vpReady {
					m.vp.SetContent(m.buildStatusContent())
				}
			} else if m.activeTab == tabLogs {
				m.logLevel = (m.logLevel + 1) % 4
				m.rebuildLogVP()
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
			} else if m.activeTab == tabRoutes && m.routeVPReady {
				m.routeVP, cmd = m.routeVP.Update(msg)
			}
			return m, cmd
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m = m.resizeViewports()

	case rulesSavedMsg:
		m.editSaving = false
		m.editMode = false
		m.editError = ""
		if m.routeVPReady {
			m.routeVP.SetContent(m.buildRoutesContent())
		}

	case rulesSaveErrMsg:
		m.editSaving = false
		m.editError = msg.err.Error()
		if m.routeVPReady {
			m.routeVP.SetContent(m.buildRoutesEditContent())
		}

	case loginRequiredMsg:
		m.err = fmt.Errorf("unauthorized — use: hopscotch status --username USER --password PASS")
		return m, tea.Quit

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
		if m.routeVPReady && !m.editMode {
			m.routeVP.SetContent(m.buildRoutesContent())
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
		for name, v := range msg.VPNs {
			if m.traffic[name] == nil {
				m.traffic[name] = &trafficWindow{}
			}
			m.traffic[name].reconnectIn = v.ReconnectIn
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
		if m.logVPReady && m.logLevelMatches(string(msg)) {
			atBottom := m.logVP.AtBottom()
			m.logVP.SetContent("  " + strings.Join(m.filteredLogLines(), "\n  "))
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
		return m, tea.Batch(fetchStatus(m.adminURL, m.httpClient), tickEvery())
	}

	return m, cmd
}

// resizeViewports recalculates both viewports for the current terminal size
// and active tab. Called on WindowSizeMsg and when switching tabs.
func (m Model) resizeViewports() Model {
	vpH := m.height - m.headerLines() - footerHeight
	if vpH < 1 {
		vpH = 1
	}

	if !m.vpReady {
		m.vp = viewport.New(m.width, vpH)
		m.vpReady = true
	} else {
		m.vp.Width = m.width
		m.vp.Height = vpH
	}
	m.vp.SetContent(m.buildStatusContent())

	logVPH := vpH - 2
	if logVPH < 1 {
		logVPH = 1
	}
	if !m.logVPReady {
		m.logVP = viewport.New(m.width, logVPH)
		m.logVPReady = true
	} else {
		m.logVP.Width = m.width
		m.logVP.Height = logVPH
	}
	if len(m.logLines) > 0 {
		atBottom := m.logVP.AtBottom()
		m.logVP.SetContent("  " + strings.Join(m.filteredLogLines(), "\n  "))
		if atBottom {
			m.logVP.GotoBottom()
		}
	}

	// Routes header adds 5 rows beyond base headerHeight (3): blank + input + result + blank + colheader + separator.
	routeVPH := vpH - 5
	if routeVPH < 1 {
		routeVPH = 1
	}
	m.routeInput.Width = m.width - 6
	if !m.routeVPReady {
		m.routeVP = viewport.New(m.width, routeVPH)
		m.routeVPReady = true
	} else {
		m.routeVP.Width = m.width
		m.routeVP.Height = routeVPH
	}
	m.routeVP.SetContent(m.buildRoutesContent())

	return m
}

func (m Model) View() string {
	if !m.ready {
		return styleMuted.Render("\n  connecting…") + "\n"
	}
	if m.err != nil {
		return styleDisconnected.Render("\n  hopscotch is not running") + "\n"
	}
	if m.width < 80 {
		msg := "↔  Terminal too narrow — please resize to at least 80 columns"
		pad := strings.Repeat("\n", max(0, m.height/2))
		indentW := (m.width - lipgloss.Width(msg)) / 2
		if indentW < 2 {
			indentW = 2
		}
		return pad + styleMuted.Render(strings.Repeat(" ", indentW)+msg) + "\n"
	}

	vp := m.vp.View()
	switch m.activeTab {
	case tabLogs:
		vp = m.logVP.View()
	case tabRoutes:
		vp = m.routeVP.View()
	}
	return m.renderHeader() + vp + m.renderFooter()
}

// ── Header renderers ──────────────────────────────────────────────────────────

// titleLeft returns the rendered meta portion of the title line (badge, uplink, PID, uptime).
func (m Model) titleLeft() string {
	versionStr := m.status.Version
	if v := m.status.LatestVersion; v != "" {
		versionStr += " " + styleConnecting.Render("⚡"+v)
	}
	uplinkStr := lipgloss.NewStyle().Foreground(colorDisconnected).Render("○ no link")
	if m.status.Uplink {
		uplinkLabel := m.status.UplinkIface
		if uplinkLabel == "" {
			uplinkLabel = "link"
		}
		label := "● " + uplinkLabel
		if m.status.UplinkIP != "" {
			label += " " + m.status.UplinkIP
		}
		uplinkStr = styleConnected.Render(label)
	}
	var internetStr string
	if m.status.PublicIP != "" {
		internetStr = "  " + styleConnected.Render("⊕ internet "+m.status.PublicIP)
	} else if m.status.Uplink && !m.status.Internet {
		internetStr = "  " + lipgloss.NewStyle().Foreground(colorDisconnected).Render("○ no internet")
	}
	return fmt.Sprintf("%s  %s%s  %s  %s",
		renderBadge(m.status.Status),
		uplinkStr,
		internetStr,
		styleMuted.Render(fmt.Sprintf("PID %d", m.status.PID)),
		styleMuted.Render("up "+m.status.Uptime),
	)
}

func (m Model) renderTitleLine() string {
	versionStr := m.status.Version
	if v := m.status.LatestVersion; v != "" {
		versionStr += " " + styleConnecting.Render("⚡"+v)
	}
	appStr := "  " + styleHeader.Render("hopscotch "+versionStr)
	meta    := m.titleLeft()
	right   := m.renderTabBar() + "  "
	rightW  := lipgloss.Width(right)

	fullLeft := appStr + "  " + meta
	if lipgloss.Width(fullLeft)+rightW <= m.width {
		// Single line: everything fits.
		gap := m.width - lipgloss.Width(fullLeft) - rightW
		if gap < 1 {
			gap = 1
		}
		return fullLeft + strings.Repeat(" ", gap) + right + "\n"
	}
	// Two lines: app name + tabs on line 1, meta on line 2.
	gap := m.width - lipgloss.Width(appStr) - rightW
	if gap < 1 {
		gap = 1
	}
	return appStr + strings.Repeat(" ", gap) + right + "\n" + "  " + meta + "\n"
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
		{"Patterns", tabRoutes},
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

	switch m.activeTab {
	case tabStatus:
		fmt.Fprintf(&b, "\n")
	case tabRoutes:
		if m.editMode {
			// Edit mode header — hints live in the footer (like vim's mode line).
			fmt.Fprintf(&b, "\n")
			if m.editSaving {
				fmt.Fprintf(&b, "  %s\n", styleConnecting.Render("saving…"))
			} else {
				fmt.Fprintf(&b, "\n")
			}
			if m.editError != "" {
				fmt.Fprintf(&b, "  %s\n", styleDisconnected.Render("✗ "+m.editError))
			} else {
				fmt.Fprintf(&b, "\n")
			}
			fmt.Fprintf(&b, "\n")
			fmt.Fprintf(&b, "  %s%s%s\n",
				styleRouteNum.Render("#"),
				styleRoutePattern.Foreground(colorMuted).Render("PATTERN"),
				lipgloss.NewStyle().Foreground(colorMuted).Width(22).Render("VIA"),
			)
			fmt.Fprintf(&b, "  %s\n", styleMuted.Render(strings.Repeat("─", m.width-4)))
		} else {
			fmt.Fprintf(&b, "\n")
			// Input line — styled by focus state.
			var inputPrefix string
			if m.routeFocused {
				inputPrefix = styleTabActive.Render("/ ")
			} else {
				inputPrefix = styleMuted.Render("/ ")
			}
			fmt.Fprintf(&b, "  %s%s\n", inputPrefix, m.routeInput.View())

			// Result line — show routing hint when input is empty.
			matchIdx := m.findRouteMatch()
			if m.routeInput.Value() == "" {
				fmt.Fprintf(&b, "  %s\n", styleMuted.Render("top to bottom · first match wins · unmatched → direct"))
			} else if matchIdx >= 0 {
				r := m.status.Routes[matchIdx]
				via := r.Tunnel
				if via == "" {
					via = r.Via
				}
				fmt.Fprintf(&b, "  %s\n",
					styleConnected.Render(fmt.Sprintf("✓ rule %d → %s", matchIdx+1, via)),
				)
			} else {
				fmt.Fprintf(&b, "  %s\n", styleMuted.Render("no rule matched → direct (fallback)"))
			}

			fmt.Fprintf(&b, "\n")
			fmt.Fprintf(&b, "  %s%s%s%s\n",
				styleRouteNum.Render("#"),
				styleRoutePattern.Foreground(colorMuted).Render("PATTERN"),
				lipgloss.NewStyle().Foreground(colorMuted).Width(22).Render("VIA"),
				styleMuted.Render("STATUS"),
			)
			fmt.Fprintf(&b, "  %s\n", styleMuted.Render(strings.Repeat("─", m.width-4)))
		}
	default:
		levelLabel := logLevelLabels[m.logLevel]
		fmt.Fprintf(&b, "\n")
		fmt.Fprintf(&b, "  %s  %s\n", styleMuted.Render("LOGS"), styleTabActive.Render(levelLabel))
		fmt.Fprintf(&b, "  %s\n", styleMuted.Render(strings.Repeat("─", m.width-4)))
	}
	return b.String()
}

// renderFooter returns a single-line bar: hints on the left, ports on the right.
func (m Model) renderFooter() string {
	hints := "q quit  tab/s/l/p switch  ↑↓/jk scroll"
	if m.activeTab == tabRoutes {
		if m.editMode {
			hints = "↑↓/jk=cursor  shift+↑↓=reorder  e/enter=edit  v=via  i=ins↑  a=add↓  d=del  ctrl+s=save  esc=cancel"
		} else if m.routeFocused {
			hints += "  esc unfocus"
		} else {
			hints += "  / test URL  e edit"
		}
	}
	if m.activeTab == tabLogs {
		hints += "  f level"
	}
	if m.activeTab == tabStatus {
		hints += "  f format"
		if m.mirrorGraph {
			hints += "  g single"
		} else {
			hints += "  g mirror"
		}
	}

	activeVP := m.vp
	switch m.activeTab {
	case tabLogs:
		activeVP = m.logVP
	case tabRoutes:
		activeVP = m.routeVP
	}
	if !activeVP.AtBottom() {
		hints += "  ↓"
	}

	proxyStr := fmt.Sprintf("PROXY %s:%d", m.status.ProxyBind, m.status.ProxyPort)
	if m.status.ProxyAuthEnabled {
		proxyStr += " ⚿"
	}
	adminStr := fmt.Sprintf("ADMIN %s:%d", m.status.AdminBind, m.status.AdminPort)
	if m.status.AdminAuthEnabled {
		adminStr += " ⚿"
	}
	ports := proxyStr + "  " + adminStr

	right := styleMuted.Render(ports)
	rightW := lipgloss.Width(right)

	editLabel := ""
	if m.editMode {
		editLabel = styleTabActive.Bold(true).Render("-- EDIT --") + "  "
	}
	// Reserve room for 2-char indent + editLabel + 2-char gap + right.
	maxHintsW := m.width - 2 - lipgloss.Width(editLabel) - 2 - rightW
	if maxHintsW < 0 {
		maxHintsW = 0
	}
	hintsRunes := []rune(hints)
	if maxHintsW > 0 && lipgloss.Width(hints) > maxHintsW {
		for len(hintsRunes) > 0 && lipgloss.Width(string(hintsRunes)) > maxHintsW-1 {
			hintsRunes = hintsRunes[:len(hintsRunes)-1]
		}
		hints = string(hintsRunes) + "…"
	}
	leftStr := editLabel + styleMuted.Render(hints)

	gap := m.width - 2 - lipgloss.Width(leftStr) - rightW
	if gap < 2 {
		gap = 2
	}

	return "\n  " + leftStr + strings.Repeat(" ", gap) + right
}


// findRouteMatch returns the index of the first rule matching the input value, or -1.
func (m Model) findRouteMatch() int {
	host := m.routeInput.Value()
	if host == "" {
		return -1
	}
	// Strip scheme if present.
	if i := strings.Index(host, "://"); i >= 0 {
		host = host[i+3:]
	}
	// Strip path/port.
	if i := strings.IndexAny(host, "/:?"); i >= 0 {
		host = host[:i]
	}
	for i, r := range m.status.Routes {
		if proxy.MatchPattern(r.Pattern, host) {
			return i
		}
	}
	return -1
}

// buildRoutesContent renders the routing rules table for the routes viewport.
func (m Model) buildRoutesContent() string {
	if len(m.status.Routes) == 0 {
		return "\n" + styleMuted.Render("  no routing rules configured") + "\n"
	}

	matchIdx := m.findRouteMatch()

	var b strings.Builder
	for i, r := range m.status.Routes {
		via := r.Tunnel
		if via == "" {
			via = r.Via
		}

		matched := matchIdx == i
		prefix := "  "
		if matched {
			prefix = styleTabActive.Render("> ")
		}

		patStyle := styleRoutePattern
		if matched {
			patStyle = styleRoutePattern.Foreground(colorAccent)
		}

		var viaRendered string
		var statusStr string
		switch {
		case via == config.ViaDirect || via == "":
			viaRendered = lipgloss.NewStyle().Foreground(colorDirect).Width(22).Render(via)
		case via == config.ViaBlock:
			viaRendered = lipgloss.NewStyle().Foreground(colorDisconnected).Width(22).Render(via)
		default:
			viaRendered = lipgloss.NewStyle().Foreground(colorAccent).Width(22).Render(via)
			if t, ok := m.status.Tunnels[via]; ok {
				statusStr = renderStatus(t.Status, m.tick, nil, t.KeepaliveFailures)
			}
		}

		fmt.Fprintf(&b, "%s%s%s%s%s\n",
			prefix,
			styleRouteNum.Render(fmt.Sprintf("%d", i+1)),
			patStyle.Render(r.Pattern),
			viaRendered,
			statusStr,
		)
	}
	return b.String()
}

// sortedTunnelNames returns tunnel names from the current status, sorted.
func (m Model) sortedTunnelNames() []string {
	names := make([]string, 0, len(m.status.Tunnels))
	for n := range m.status.Tunnels {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// editInsertRule inserts a new empty rule at idx, sets cursor there, opens pattern input.
func (m *Model) editInsertRule(idx int) {
	if idx > len(m.editRules) {
		idx = len(m.editRules)
	}
	newRule := editRule{isNew: true}
	tail := make([]editRule, len(m.editRules[idx:]))
	copy(tail, m.editRules[idx:])
	m.editRules = append(m.editRules[:idx], append([]editRule{newRule}, tail...)...)
	m.editCursor = idx
	m.editPatInput.SetValue("")
	m.editPatFocused = true
	m.editPatInput.Focus()
	if m.routeVPReady {
		m.routeVP.SetContent(m.buildRoutesEditContent())
	}
}

// editCycleVia cycles the via of the currently selected edit rule through
// "direct" → tunnel names → "block".
func (m *Model) editCycleVia() {
	if m.editCursor >= len(m.editRules) {
		return
	}
	r := &m.editRules[m.editCursor]
	options := append([]string{config.ViaDirect}, append(m.sortedTunnelNames(), config.ViaBlock)...)
	current := r.Via
	if current == "" {
		current = r.Tunnel
	}
	if current == "" {
		current = config.ViaDirect
	}
	for i, opt := range options {
		if opt == current {
			next := options[(i+1)%len(options)]
			switch next {
			case config.ViaDirect:
				r.Tunnel = ""
				r.Via = config.ViaDirect
			case config.ViaBlock:
				r.Tunnel = ""
				r.Via = config.ViaBlock
			default:
				r.Tunnel = next
				r.Via = ""
			}
			r.recomputeModified()
			return
		}
	}
	r.Tunnel = ""
	r.Via = config.ViaDirect
	r.recomputeModified()
}

// buildRoutesEditContent renders the editable rule table for the routes viewport.
func (m Model) buildRoutesEditContent() string {
	if len(m.editRules) == 0 {
		return "\n" + styleMuted.Render("  no rules — press a to add") + "\n"
	}

	var b strings.Builder
	for i, r := range m.editRules {
		via := r.Tunnel
		if via == "" {
			via = r.Via
		}
		if via == "" {
			via = config.ViaDirect
		}

		selected := m.editCursor == i

		// Prefix: cursor glyph with colour indicating row state
		var prefix string
		switch {
		case selected && r.isNew:
			prefix = styleEditNew.Bold(true).Render("> ")
		case selected && r.isDeleted:
			prefix = styleEditDeleted.Bold(true).Render("> ")
		case selected:
			prefix = styleTabActive.Render("> ")
		case r.isNew:
			prefix = styleEditNew.Render("+ ")
		case r.isDeleted:
			prefix = styleEditDeleted.Render("- ")
		default:
			prefix = "  "
		}

		// Pattern
		var patRendered string
		switch {
		case selected && m.editPatFocused:
			patRendered = styleRoutePattern.Render(m.editPatInput.View())
		case r.isDeleted:
			patRendered = styleRoutePattern.Foreground(colorDisconnected).Strikethrough(true).Render(r.Pattern)
		case r.isNew:
			patRendered = styleRoutePattern.Foreground(colorConnected).Render(r.Pattern)
		case r.isModified && selected:
			patRendered = styleRoutePattern.Foreground(colorConnecting).Render(r.Pattern)
		case r.isModified:
			patRendered = styleRoutePattern.Foreground(colorConnecting).Render(r.Pattern)
		case selected:
			patRendered = styleRoutePattern.Foreground(colorAccent).Render(r.Pattern)
		default:
			patRendered = styleRoutePattern.Render(r.Pattern)
		}

		// Via
		var viaRendered string
		viaW := lipgloss.NewStyle().Width(22)
		switch {
		case r.isDeleted:
			viaRendered = viaW.Foreground(colorDisconnected).Strikethrough(true).Render(via)
		case via == config.ViaBlock:
			viaRendered = viaW.Foreground(colorDisconnected).Render(via)
		case r.isNew:
			viaRendered = viaW.Foreground(colorConnected).Render(via)
		case r.isModified:
			viaRendered = viaW.Foreground(colorConnecting).Render(via)
		case r.Tunnel != "":
			viaRendered = viaW.Foreground(colorAccent).Render(via)
		default: // "direct"
			viaRendered = viaW.Foreground(colorDirect).Render(via)
		}

		// Inline validation error — same line, after via, truncated to fit
		var errStr string
		if r.validErr != "" && !r.isDeleted {
			// 2 prefix + 4 num + 32 pattern + 22 via = 60 used; leave 4 margin
			avail := m.width - 60 - 4
			if avail > 6 {
				msg := "✗ " + r.validErr
				if lipgloss.Width(msg) > avail {
					msg = string([]rune(msg)[:avail-1]) + "…"
				}
				errStr = lipgloss.NewStyle().Foreground(colorDisconnected).Render(msg)
			}
		}

		fmt.Fprintf(&b, "%s%s%s%s%s\n",
			prefix,
			styleRouteNum.Render(fmt.Sprintf("%d", i+1)),
			patRendered,
			viaRendered,
			errStr,
		)
	}
	return b.String()
}

// saveRulesCmd returns a Cmd that PUTs the current editRules to the admin API.
func (m Model) saveRulesCmd() tea.Cmd {
	var rules []admin.RouteJSON
	for _, r := range m.editRules {
		if !r.isDeleted {
			rules = append(rules, r.RouteJSON)
		}
	}
	if rules == nil {
		rules = []admin.RouteJSON{}
	}
	rulesURL := strings.TrimSuffix(m.adminURL, "/status") + "/api/rules"
	client := m.httpClient
	return func() tea.Msg {
		body, err := json.Marshal(map[string]interface{}{"rules": rules})
		if err != nil {
			return rulesSaveErrMsg{err}
		}
		req, err := http.NewRequest(http.MethodPut, rulesURL, bytes.NewReader(body))
		if err != nil {
			return rulesSaveErrMsg{err}
		}
		req.Header.Set("Content-Type", "application/json")
		res, err := client.Do(req)
		if err != nil {
			return rulesSaveErrMsg{err}
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			var msg [256]byte
			n, _ := res.Body.Read(msg[:])
			return rulesSaveErrMsg{fmt.Errorf("%s", strings.TrimSpace(string(msg[:n])))}
		}
		return rulesSavedMsg{}
	}
}

// Fixed column widths that never change with terminal size.
const (
	colIndent  = 2
	colVPNW    = 14 // VPN label col in tunnel rows; also IFACE col in VPN rows
	colPortW   = 7
	colStatusW = 20
	colUptimeW = 10
	colRCW     = 5
	colBpsInW  = 15
	colBpsOutW = 15
	colConnW   = 8
	colNameMin = 10
	colHostMin = 10
)

// colLayout holds the computed visible-column set and elastic widths for one render pass.
type colLayout struct {
	nameW      int
	hostW      int
	showHost   bool
	showPort   bool
	showUptime bool
	showRC     bool
	showBPS    bool // bpsIn + bpsOut together
	showConn   bool
}

// layoutTunnelCols derives which columns to show and name/host widths for tunnel rows.
//
//	w ≥ 144 → all cols, name+host elastic
//	w ≥ 130 → drop conn
//	w ≥ 110 → drop conn + bps
//	w ≥  98 → drop conn + bps + rc
//	w ≥  85 → drop conn + bps + rc + port
//	w ≥  70 → drop conn + bps + rc + port + uptime
//	w  < 70 → name + vpn + status only
func layoutTunnelCols(width int) colLayout {
	var l colLayout
	l.showHost   = width >= 70
	l.showPort   = width >= 85
	l.showUptime = width >= 70
	l.showRC     = width >= 98
	l.showBPS    = width >= 110
	l.showConn   = width >= 144

	fixed := colIndent + colVPNW + colStatusW
	if l.showPort   { fixed += colPortW }
	if l.showUptime { fixed += colUptimeW }
	if l.showRC     { fixed += colRCW }
	if l.showBPS    { fixed += colBpsInW + colBpsOutW }
	if l.showConn   { fixed += colConnW }

	remaining := width - fixed
	if l.showHost {
		l.nameW = remaining * 55 / 100
		if l.nameW < colNameMin {
			l.nameW = colNameMin
		}
		l.hostW = remaining - l.nameW
		if l.hostW < colHostMin {
			l.hostW = colHostMin
			l.nameW = remaining - l.hostW
			if l.nameW < colNameMin {
				l.nameW = colNameMin
			}
		}
	} else {
		l.nameW = remaining
		if l.nameW < colNameMin {
			l.nameW = colNameMin
		}
	}
	return l
}

// layoutVPNCols derives column layout for VPN rows (no bps/conn).
// nameW/hostW are taken from the tunnel layout so the two sections stay column-aligned.
func layoutVPNCols(width int, tl colLayout) colLayout {
	return colLayout{
		nameW:      tl.nameW,
		hostW:      tl.hostW,
		showHost:   width >= 70,
		showPort:   width >= 85,
		showUptime: width >= 70,
		showRC:     width >= 98,
	}
}

func (m Model) sectionSep() string {
	return "  " + styleMuted.Render(strings.Repeat("─", max(m.width-4, 10))) + "\n"
}

func (m Model) buildStatusContent() string {
	tl := layoutTunnelCols(m.width)
	vl := layoutVPNCols(m.width, tl)

	sparkW := m.width - 4
	if sparkW < 10 {
		sparkW = 10
	}

	// Per-render styles — widths computed from terminal size.
	tName   := lipgloss.NewStyle().Foreground(colorBright).Width(tl.nameW)
	tHost   := lipgloss.NewStyle().Foreground(colorMuted).Width(tl.hostW)
	tVPN    := lipgloss.NewStyle().Width(colVPNW)
	tPort   := lipgloss.NewStyle().Foreground(colorText).Width(colPortW)
	tStatus := lipgloss.NewStyle().Width(colStatusW)
	tUptime := lipgloss.NewStyle().Foreground(colorText).Width(colUptimeW)
	tRC     := lipgloss.NewStyle().Foreground(colorMuted).Width(colRCW)
	tBpsIn  := lipgloss.NewStyle().Foreground(colorBpsIn).Width(colBpsInW)
	tBpsOut := lipgloss.NewStyle().Foreground(colorBpsOut).Width(colBpsOutW)
	tConn   := lipgloss.NewStyle().Foreground(colorText).Width(colConnW)

	vName   := lipgloss.NewStyle().Foreground(colorBright).Width(vl.nameW)
	vHost   := lipgloss.NewStyle().Foreground(colorMuted).Width(vl.hostW)
	vIface  := lipgloss.NewStyle().Foreground(colorMuted).Width(colVPNW)
	vPort   := lipgloss.NewStyle().Foreground(colorText).Width(colPortW)
	vStatus := lipgloss.NewStyle().Width(colStatusW)
	vUptime := lipgloss.NewStyle().Foreground(colorText).Width(colUptimeW)
	vRC     := lipgloss.NewStyle().Foreground(colorMuted).Width(colRCW)

	hdr := func(s lipgloss.Style, label string) string {
		return s.Foreground(colorMuted).Render(label)
	}

	var b strings.Builder

	// ── VPN section ───────────────────────────────────────────────────────────
	if len(m.status.VPNs) > 0 {
		b.WriteString("  ")
		b.WriteString(hdr(vName, "VPN"))
		if vl.showHost   { b.WriteString(hdr(vHost, "HOST")) }
		b.WriteString(hdr(vIface, "IFACE"))
		if vl.showPort   { b.WriteString(hdr(vPort, "")) }
		b.WriteString(hdr(vStatus, "STATUS"))
		if vl.showUptime { b.WriteString(hdr(vUptime, "UPTIME")) }
		if vl.showRC     { b.WriteString(hdr(vRC, "RC")) }
		b.WriteString("\n")
		b.WriteString(m.sectionSep())

		vpnNames := make([]string, 0, len(m.status.VPNs))
		for name := range m.status.VPNs {
			vpnNames = append(vpnNames, name)
		}
		sort.Strings(vpnNames)

		for _, name := range vpnNames {
			v := m.status.VPNs[name]
			w := m.traffic[name]

			var reconnectIn *int
			if w != nil {
				reconnectIn = w.reconnectIn
			}

			uptime := "—"
			if v.UptimeSeconds > 0 {
				uptime = fmtDuration(time.Duration(v.UptimeSeconds) * time.Second)
			}

			iface := v.TunIface
			if iface == "" {
				iface = "—"
			}

			b.WriteString("  ")
			b.WriteString(vName.Foreground(colorVPN).Render(name))
			if vl.showHost   { b.WriteString(vHost.Render(v.Host)) }
			b.WriteString(vIface.Render(iface))
			if vl.showPort   { b.WriteString(vPort.Render("")) }
			b.WriteString(vStatus.Render(renderStatus(v.State, m.tick, reconnectIn, 0)))
			if vl.showUptime { b.WriteString(vUptime.Render(uptime)) }
			if vl.showRC     { b.WriteString(vRC.Render(fmt.Sprintf("%d", v.Reconnects))) }
			b.WriteString("\n")

			if v.LastError != "" && v.State != msgs.StatusConnected {
				if isVPNProgressMsg(v.LastError) {
					b.WriteString(renderErrMsg("◌ ", v.LastError, styleConnecting, m.width) + "\n")
				} else {
					b.WriteString(renderErrMsg("└ ✗ ", v.LastError, lipgloss.NewStyle().Foreground(colorDisconnected), m.width) + "\n")
				}
			}
		}
		b.WriteString("\n")
	}

	// ── Tunnel section ────────────────────────────────────────────────────────
	b.WriteString("  ")
	b.WriteString(hdr(tName, "TUNNEL"))
	if tl.showHost   { b.WriteString(hdr(tHost, "HOST")) }
	b.WriteString(hdr(tVPN, "VPN"))
	if tl.showPort   { b.WriteString(hdr(tPort, "PORT")) }
	b.WriteString(hdr(tStatus, "STATUS"))
	if tl.showUptime { b.WriteString(hdr(tUptime, "UPTIME")) }
	if tl.showRC     { b.WriteString(hdr(tRC, "RC")) }
	if tl.showBPS    { b.WriteString(hdr(tBpsIn, "↓")); b.WriteString(hdr(tBpsOut, "↑")) }
	if tl.showConn   { b.WriteString(hdr(tConn, "CONN")) }
	b.WriteString("\n")
	b.WriteString(m.sectionSep())

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

		// error sub-line: propagate VPN root cause so "waiting for VPN: X" shows X's actual reason.
		dispErr := t.LastError
		if strings.HasPrefix(t.LastError, msgs.WaitingForVPNPrefix) {
			vpnName := strings.TrimPrefix(t.LastError, msgs.WaitingForVPNPrefix)
			if v, ok := m.status.VPNs[vpnName]; ok && v.LastError != "" {
				dispErr = v.LastError
			}
		}
		errLine := ""
		if dispErr != "" && t.Status != msgs.StatusConnected {
			if strings.HasPrefix(dispErr, msgs.WaitingForVPNPrefix) || dispErr == msgs.WaitingForNetwork {
				errLine = renderErrMsg("◌ ", dispErr, styleConnecting, m.width)
			} else {
				errLine = renderErrMsg("└ ✗ ", dispErr, lipgloss.NewStyle().Foreground(colorDisconnected), m.width)
			}
		}

		var tunnelStatusStr string
		if strings.HasPrefix(t.LastError, msgs.WaitingForVPNPrefix) || t.LastError == msgs.WaitingForNetwork {
			tunnelStatusStr = styleConnecting.Render("◌ pending")
		} else {
			tunnelStatusStr = renderStatus(t.Status, m.tick, reconnectIn, t.KeepaliveFailures)
		}

		vpnLabel := "—"
		vpnColStyle := tVPN.Foreground(colorMuted)
		if t.RequiresVPN != "" {
			bullet := "○"
			if v, ok := m.status.VPNs[t.RequiresVPN]; ok {
				switch v.State {
				case msgs.StatusConnected:
					bullet = "●"
					vpnColStyle = tVPN.Foreground(colorConnected)
				case msgs.StatusConnecting:
					vpnColStyle = tVPN.Foreground(colorConnecting)
				default:
					vpnColStyle = tVPN.Foreground(colorDisconnected)
				}
			}
			vpnLabel = bullet + " " + t.RequiresVPN
		}

		b.WriteString("  ")
		b.WriteString(tName.Foreground(colorAccent).Render(name))
		if tl.showHost   { b.WriteString(tHost.Render(t.Host)) }
		b.WriteString(vpnColStyle.Render(vpnLabel))
		if tl.showPort   { b.WriteString(tPort.Render(fmt.Sprintf("%d", t.LocalPort))) }
		b.WriteString(tStatus.Render(tunnelStatusStr))
		if tl.showUptime { b.WriteString(tUptime.Render(uptime)) }
		if tl.showRC     { b.WriteString(tRC.Render(fmt.Sprintf("%d", t.ReconnectCount))) }
		if tl.showBPS    { b.WriteString(tBpsIn.Render("↓ "+fmtBytes(bpsIn))); b.WriteString(tBpsOut.Render("↑ "+fmtBytes(bpsOut))) }
		if tl.showConn   { b.WriteString(tConn.Render(fmtActive(active))) }
		b.WriteString("\n")

		if errLine != "" {
			fmt.Fprintf(&b, "%s\n", errLine)
		}

		if !m.compact && w != nil {
			for _, line := range renderGraph(w.dataIn, w.dataOut, colorBpsIn, colorBpsOut, graphRows, sparkW, m.mirrorGraph) {
				fmt.Fprintf(&b, "  %s\n", line)
			}
		}
	}

	// direct row
	dw := m.traffic["direct"]
	var dBpsIn, dBpsOut uint64
	var dActive int64
	if dw != nil {
		dBpsIn = dw.bpsIn
		dBpsOut = dw.bpsOut
		dActive = dw.active
	}
	b.WriteString("  ")
	b.WriteString(tName.Foreground(colorDirect).Render("direct"))
	if tl.showHost   { b.WriteString(tHost.Render("")) }
	b.WriteString(tVPN.Render(""))
	if tl.showPort   { b.WriteString(tPort.Render("")) }
	b.WriteString(tStatus.Render(""))
	if tl.showUptime { b.WriteString(tUptime.Render("")) }
	if tl.showRC     { b.WriteString(tRC.Render("")) }
	if tl.showBPS    { b.WriteString(tBpsIn.Render("↓ "+fmtBytes(dBpsIn))); b.WriteString(tBpsOut.Render("↑ "+fmtBytes(dBpsOut))) }
	if tl.showConn   { b.WriteString(tConn.Render(fmtActive(dActive))) }
	b.WriteString("\n")

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
	return fmt.Sprintf("%d", n)
}

// renderErrMsg renders a prefixed error message with line-wrapping.
// Continuation lines are indented to align with the message start.
func renderErrMsg(prefix, msg string, style lipgloss.Style, termWidth int) string {
	prefixRunes := len([]rune(prefix))
	lineW := termWidth - 4 - prefixRunes // 4 = leading indent spaces
	if lineW < 10 {
		return "    " + style.Render(prefix+msg)
	}
	lines := wrapAt(msg, lineW)
	contIndent := strings.Repeat(" ", 4+prefixRunes)
	var b strings.Builder
	b.WriteString("    " + style.Render(prefix+lines[0]))
	for _, l := range lines[1:] {
		b.WriteString("\n" + contIndent + style.Render(l))
	}
	return b.String()
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
	case msgs.StatusConnected:
		if keepaliveFails > 0 {
			return styleConnecting.Render(fmt.Sprintf("● connected ⚠%d", keepaliveFails))
		}
		return styleConnected.Render("● connected")
	case msgs.StatusConnecting:
		dot := "●"
		if tick%2 == 0 {
			dot = "○"
		}
		if reconnectIn != nil && *reconnectIn >= 0 {
			return styleConnecting.Render(fmt.Sprintf("%s next try: %ds", dot, *reconnectIn))
		}
		return styleConnecting.Render(dot + " connecting")
	case msgs.StatusDisconnected:
		if reconnectIn != nil && *reconnectIn >= 0 {
			return styleConnecting.Render(fmt.Sprintf("○ next try: %ds", *reconnectIn))
		}
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

// isVPNProgressMsg returns true for informational VPN connecting-phase messages
// that should be shown in amber (not red). These are set by hopscotch itself
// during connect steps, as opposed to errors coming from the subprocess.
func isVPNProgressMsg(msg string) bool {
	return strings.HasPrefix(msg, "resolving ") ||
		strings.HasPrefix(msg, "DNS retry: ") ||
		strings.HasPrefix(msg, "pre_connect: ") ||
		strings.HasPrefix(msg, "probing ") ||
		msg == "openconnect starting" ||
		msg == msgs.WaitingForVPNTunnel ||
		msg == msgs.WaitingForNetwork
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

// ── Log level filter ──────────────────────────────────────────────────────────

var logLevelLabels = []string{"ALL", "INFO+", "WARN+", "ERR"}
var logLevelTokens = []string{"", " INFO ", " WARN ", " ERRO "}

func (m Model) logLevelMatches(line string) bool {
	if m.logLevel == 0 {
		return true
	}
	// Strip ANSI and check for level tokens at or above current filter.
	stripped := ansiRe.ReplaceAllString(line, "")
	for i := m.logLevel; i < len(logLevelTokens); i++ {
		if logLevelTokens[i] != "" && strings.Contains(stripped, logLevelTokens[i]) {
			return true
		}
	}
	return false
}

func (m Model) filteredLogLines() []string {
	if m.logLevel == 0 {
		return m.logLines
	}
	out := make([]string, 0, len(m.logLines))
	for _, l := range m.logLines {
		if m.logLevelMatches(l) {
			out = append(out, l)
		}
	}
	return out
}

func (m *Model) rebuildLogVP() {
	if !m.logVPReady {
		return
	}
	atBottom := m.logVP.AtBottom()
	m.logVP.SetContent("  " + strings.Join(m.filteredLogLines(), "\n  "))
	if atBottom {
		m.logVP.GotoBottom()
	}
}

// ── Commands ──────────────────────────────────────────────────────────────────

func tickEvery() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

type loginRequiredMsg struct{}

func fetchStatus(url string, client *http.Client) tea.Cmd {
	return func() tea.Msg {
		resp, err := client.Get(url) //nolint:noctx
		if err != nil {
			return errMsg(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusUnauthorized {
			return loginRequiredMsg{}
		}
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

func runSSE(url string, client *http.Client, ch chan<- ssePayload, done <-chan struct{}) {
	for {
		select {
		case <-done:
			return
		default:
		}

		resp, err := client.Get(url) //nolint:noctx
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

func runLogSSE(url string, client *http.Client, ch chan<- string, done <-chan struct{}) {
	for {
		select {
		case <-done:
			return
		default:
		}

		resp, err := client.Get(url) //nolint:noctx
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
