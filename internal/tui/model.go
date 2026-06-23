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
	styleColPort   = lipgloss.NewStyle().Foreground(colorText).Width(7)
	styleColStatus = lipgloss.NewStyle().Width(16)
	styleColUptime = lipgloss.NewStyle().Foreground(colorText).Width(10)
)

// ── Traffic data ──────────────────────────────────────────────────────────────

const windowSize = 300

var blocks = []rune("⣀⣄⣤⣦⣶⣷⣿")

type trafficWindow struct {
	data        []float64
	bpsIn       uint64
	bpsOut      uint64
	active      int64
	reconnectIn *int
}

func (w *trafficWindow) push(bpsIn, bpsOut uint64) {
	w.bpsIn = bpsIn
	w.bpsOut = bpsOut
	w.data = append(w.data, float64(bpsIn+bpsOut))
	if len(w.data) > windowSize {
		w.data = w.data[1:]
	}
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

func sparkline(w *trafficWindow, color lipgloss.Color, width int) string {
	if width <= 0 {
		return ""
	}

	padded := make([]float64, width)
	if len(w.data) > 0 {
		if len(w.data) < width {
			copy(padded[width-len(w.data):], w.data)
		} else {
			copy(padded, w.data[len(w.data)-width:])
		}
	}

	if len(w.data) == 0 {
		dim := lipgloss.NewStyle().Foreground(colorMuted)
		var sb strings.Builder
		for range padded {
			sb.WriteString(dim.Render(string(blocks[0])))
		}
		return sb.String()
	}

	maxVal := 0.0
	for _, v := range padded {
		if v > maxVal {
			maxVal = v
		}
	}

	n := len(padded)
	var sb strings.Builder
	for i, v := range padded {
		idx := 0
		if maxVal > 0 {
			idx = int(math.Round(v / maxVal * float64(len(blocks)-1)))
		}
		t := float64(i) / float64(max(n-1, 1))
		c := blendColor(colorMuted, color, t)
		sb.WriteString(lipgloss.NewStyle().Foreground(c).Render(string(blocks[idx])))
	}
	return sb.String()
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

type statusMsg admin.StatusResponse
type sseMsg    ssePayload
type errMsg    error
type tickMsg   time.Time

// ── Layout constants ──────────────────────────────────────────────────────────

const (
	headerHeight = 6 // blank + title + stats + blank + colheaders + separator
	footerHeight = 4 // blank + ports + blank + hints
)

// ── Model ─────────────────────────────────────────────────────────────────────

type Model struct {
	status  admin.StatusResponse
	traffic map[string]*trafficWindow
	err     error

	adminURL string
	sseURL   string
	sseCh    chan ssePayload
	done     chan struct{}

	tick    int
	width   int
	height  int
	vp      viewport.Model
	vpReady bool
	compact bool
	ready   bool
}

func New(adminURL string) Model {
	sseURL := strings.TrimSuffix(adminURL, "/status") + "/traffic/stream"
	ch := make(chan ssePayload, 8)
	done := make(chan struct{})
	return Model{
		adminURL: adminURL,
		sseURL:   sseURL,
		sseCh:    ch,
		done:     done,
		traffic:  make(map[string]*trafficWindow),
		width:    80,
		height:   24,
	}
}

func (m Model) Init() tea.Cmd {
	go runSSE(m.sseURL, m.sseCh, m.done)
	return tea.Batch(fetchStatus(m.adminURL), tickEvery(), waitForSSE(m.sseCh))
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "Q", "ctrl+c", "esc":
			close(m.done)
			return m, tea.Quit
		case "c", "C":
			m.compact = !m.compact
			if m.vpReady {
				m.vp.SetContent(m.buildContent())
			}
			return m, nil
		default:
			if m.vpReady {
				m.vp, cmd = m.vp.Update(msg)
			}
			return m, cmd
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		vpH := msg.Height - headerHeight - footerHeight
		if vpH < 1 {
			vpH = 1
		}
		if !m.vpReady {
			m.vp = viewport.New(msg.Width, vpH)
			m.vpReady = true
		} else {
			m.vp.Width = msg.Width
			m.vp.Height = vpH
		}
		m.vp.SetContent(m.buildContent())

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
			m.vp.SetContent(m.buildContent())
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
			m.vp.SetContent(m.buildContent())
		}
		return m, waitForSSE(m.sseCh)

	case errMsg:
		m.err = msg
		m.ready = true

	case tickMsg:
		m.tick++
		if m.vpReady {
			m.vp.SetContent(m.buildContent())
		}
		return m, tea.Batch(fetchStatus(m.adminURL), tickEvery())
	}

	return m, cmd
}

func (m Model) View() string {
	if !m.ready {
		return styleMuted.Render("\n  connecting…") + "\n"
	}
	if m.err != nil {
		return styleDisconnected.Render("\n  hopscotch is not running") + "\n"
	}
	return m.renderHeader() + m.vp.View() + m.renderFooter()
}

// renderHeader returns the fixed top block (headerHeight lines).
func (m Model) renderHeader() string {
	var b strings.Builder

	// title line
	fmt.Fprintf(&b, "\n  %s  %s  %s  %s\n",
		styleHeader.Render("hopscotch "+m.status.Version),
		renderBadge(m.status.Status),
		styleMuted.Render(fmt.Sprintf("PID %d", m.status.PID)),
		styleMuted.Render("up "+m.status.Uptime),
	)

	// global stats line
	totalIn, totalOut, totalActive := m.totalStats()
	fmt.Fprintf(&b, "  %s  %s  %s\n",
		lipgloss.NewStyle().Foreground(colorBpsIn).Render(fmt.Sprintf("↓ %-12s", fmtBytes(totalIn))),
		lipgloss.NewStyle().Foreground(colorBpsOut).Render(fmt.Sprintf("↑ %-12s", fmtBytes(totalOut))),
		styleText.Render(fmt.Sprintf("%d conn total", totalActive)),
	)

	// column headers
	fmt.Fprintln(&b)
	colNameMuted   := styleColName.Foreground(colorMuted)
	colPortMuted   := styleColPort.Foreground(colorMuted)
	colStatusMuted := styleColStatus.Foreground(colorMuted)
	colUptimeMuted := styleColUptime.Foreground(colorMuted)
	fmt.Fprintf(&b, "  %s%s%s%s%s\n",
		colNameMuted.Render("TUNNEL"),
		colPortMuted.Render("PORT"),
		colStatusMuted.Render("STATUS"),
		colUptimeMuted.Render("UPTIME"),
		styleMuted.Render("RECONNECTS"),
	)
	fmt.Fprintf(&b, "  %s\n", styleMuted.Render(strings.Repeat("─", m.width-4)))

	return b.String()
}

// renderFooter returns the fixed bottom block (footerHeight lines).
func (m Model) renderFooter() string {
	var b strings.Builder

	scrollHint := ""
	if m.vpReady && !m.vp.AtBottom() {
		scrollHint = "  " + styleMuted.Render("↓ scroll")
	} else if m.vpReady && !m.vp.AtTop() {
		scrollHint = "  " + styleMuted.Render("↑ scroll")
	}

	fmt.Fprintf(&b, "\n  %s %s    %s %s%s\n",
		styleMuted.Render("PROXY"), styleAccent.Render(fmt.Sprintf(":%d", m.status.ProxyPort)),
		styleMuted.Render("ADMIN"), styleAccent.Render(fmt.Sprintf(":%d", m.status.AdminPort)),
		scrollHint,
	)

	compactLabel := "compact"
	if m.compact {
		compactLabel = "expand"
	}
	fmt.Fprintf(&b, "\n  %s\n", styleMuted.Render(fmt.Sprintf("q quit  ↑↓/jk scroll  c %s", compactLabel)))

	return b.String()
}

// buildContent renders the scrollable tunnel list for the viewport.
func (m Model) buildContent() string {
	sparkW := m.width - 39
	if sparkW < 10 {
		sparkW = 10
	}

	var b strings.Builder

	names := make([]string, 0, len(m.status.Tunnels))
	for name := range m.status.Tunnels {
		names = append(names, name)
	}
	sort.Strings(names)

	for i, name := range names {
		t := m.status.Tunnels[name]
		color := palette[i%len(palette)]
		w := m.traffic[name]

		uptime := "—"
		if t.UptimeSeconds > 0 {
			uptime = fmtDuration(time.Duration(t.UptimeSeconds) * time.Second)
		}

		var reconnectIn *int
		if w != nil {
			reconnectIn = w.reconnectIn
		}

		fmt.Fprintf(&b, "  %s%s%s%s%s\n",
			styleColName.Render(name),
			styleColPort.Render(fmt.Sprintf("%d", t.LocalPort)),
			styleColStatus.Render(renderStatus(t.Status, m.tick, reconnectIn, t.KeepaliveFailures)),
			styleColUptime.Render(uptime),
			styleMuted.Render(fmt.Sprintf("%d", t.ReconnectCount)),
		)

		if !m.compact && w != nil {
			fmt.Fprintf(&b, "  %s  %s  %s  %s\n",
				lipgloss.NewStyle().Foreground(colorBpsIn).Render(fmt.Sprintf("↓ %-10s", fmtBytes(w.bpsIn))),
				lipgloss.NewStyle().Foreground(colorBpsOut).Render(fmt.Sprintf("↑ %-10s", fmtBytes(w.bpsOut))),
				styleText.Render(fmt.Sprintf("%-7s", fmtActive(w.active))),
				sparkline(w, color, sparkW),
			)
		}

		fmt.Fprintln(&b)
	}

	// direct
	dw := m.traffic["direct"]
	fmt.Fprintf(&b, "  %s\n",
		styleColName.Foreground(colorMuted).Render("direct"),
	)
	if !m.compact && dw != nil {
		fmt.Fprintf(&b, "  %s  %s  %s  %s\n",
			lipgloss.NewStyle().Foreground(colorBpsIn).Render(fmt.Sprintf("↓ %-10s", fmtBytes(dw.bpsIn))),
			lipgloss.NewStyle().Foreground(colorBpsOut).Render(fmt.Sprintf("↑ %-10s", fmtBytes(dw.bpsOut))),
			styleText.Render(fmt.Sprintf("%-7s", fmtActive(dw.active))),
			sparkline(dw, directColor, sparkW),
		)
	}
	fmt.Fprintln(&b)

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
			return styleConnecting.Render(fmt.Sprintf("● connected ⚠%d", keepaliveFails))
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
