package panels

import (
	"fmt"
	"strings"

	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/tui/datasource"
	"github.com/Constellation-Overwatch/constellation-overwatch/pkg/tui/styles"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// SystemModel displays system metrics (memory, CPU, goroutines)
type SystemModel struct {
	memTotal       uint64
	memAlloc       uint64
	heapAlloc      uint64
	numGoroutines  int
	numCPU         int
	numGC          uint32
	cpuPercent     float64
	memUsedPercent float64
	loadAvg1       float64

	width   int
	height  int
	focused bool
}

// NewSystemModel creates a new system metrics panel
func NewSystemModel() SystemModel {
	return SystemModel{}
}

func (m SystemModel) Init() tea.Cmd {
	return nil
}

func (m SystemModel) Update(msg tea.Msg) (SystemModel, tea.Cmd) {
	switch msg := msg.(type) {
	case datasource.MetricsSnapshot:
		m.memTotal = msg.MemTotal
		m.memAlloc = msg.MemAlloc
		m.heapAlloc = msg.HeapAlloc
		m.numGoroutines = msg.NumGoroutines
		m.numCPU = msg.NumCPU
		m.numGC = msg.NumGC
		m.cpuPercent = msg.CPUPercent
		m.memUsedPercent = msg.MemUsedPercent
		m.loadAvg1 = msg.LoadAvg1
	}
	return m, nil
}

func (m SystemModel) View() string {
	// Calculate gauge widths based on available space
	gaugeWidth := m.width - 14 // Account for labels and padding
	if gaugeWidth < 10 {
		gaugeWidth = 10
	}

	// Calculate percentages
	var memPercent, heapPercent float64
	if m.memTotal > 0 {
		memPercent = float64(m.memAlloc) / float64(m.memTotal) * 100
		heapPercent = float64(m.heapAlloc) / float64(m.memTotal) * 100
	}

	// Build content
	var content strings.Builder

	// CPU% gauge (system-level)
	content.WriteString(m.renderGauge("CPU%", m.cpuPercent, gaugeWidth))
	content.WriteString("\n")

	// OS Memory gauge (system-level)
	content.WriteString(m.renderGauge("OS Mem", m.memUsedPercent, gaugeWidth))
	content.WriteString("\n")

	// Load average
	loadLabel := styles.GaugeLabelStyle.Render("Load:")
	loadValue := styles.GaugeValueStyle.Render(fmt.Sprintf("%.2f", m.loadAvg1))
	content.WriteString(fmt.Sprintf("%s %s", loadLabel, loadValue))
	content.WriteString("\n")

	// Memory gauge (Go runtime)
	content.WriteString(m.renderGauge("Mem", memPercent, gaugeWidth))
	content.WriteString("\n")

	// Heap gauge (Go runtime)
	content.WriteString(m.renderGauge("Heap", heapPercent, gaugeWidth))
	content.WriteString("\n")

	// Goroutines (numeric display)
	goRoutineLabel := styles.GaugeLabelStyle.Render("Gort:")
	goRoutineValue := styles.GaugeValueStyle.Render(fmt.Sprintf("%d", m.numGoroutines))
	content.WriteString(fmt.Sprintf("%s %s", goRoutineLabel, goRoutineValue))
	content.WriteString("\n")

	// CPU cores
	cpuLabel := styles.GaugeLabelStyle.Render("CPU:")
	cpuValue := styles.GaugeValueStyle.Render(fmt.Sprintf("%d cores", m.numCPU))
	content.WriteString(fmt.Sprintf("%s %s", cpuLabel, cpuValue))
	content.WriteString("\n")

	// GC cycles
	gcLabel := styles.GaugeLabelStyle.Render("GC:")
	gcValue := styles.StatusMutedStyle.Render(fmt.Sprintf("%d cycles", m.numGC))
	content.WriteString(fmt.Sprintf("%s %s", gcLabel, gcValue))

	return content.String()
}

func (m SystemModel) renderGauge(label string, percent float64, width int) string {
	if percent > 100 {
		percent = 100
	}
	if percent < 0 {
		percent = 0
	}

	filled := int(float64(width) * percent / 100)
	empty := width - filled

	labelStr := styles.GaugeLabelStyle.Render(label + ":")
	filledBar := styles.GaugeFilledStyle.Render(strings.Repeat("=", filled))
	emptyBar := styles.GaugeEmptyStyle.Render(strings.Repeat("-", empty))
	percentStr := styles.GaugeValueStyle.Render(fmt.Sprintf("%5.1f%%", percent))

	return fmt.Sprintf("%s [%s%s] %s", labelStr, filledBar, emptyBar, percentStr)
}

// SetSize sets the panel dimensions
func (m *SystemModel) SetSize(width, height int) {
	m.width = width
	m.height = height
}

// SetFocused sets whether this panel is focused
func (m *SystemModel) SetFocused(focused bool) {
	m.focused = focused
}

// Focused returns whether this panel is focused
func (m SystemModel) Focused() bool {
	return m.focused
}

// Title returns the panel title
func (m SystemModel) Title() string {
	return "SYSTEM METRICS"
}

// RenderWithBorder renders the panel with its border
func (m SystemModel) RenderWithBorder() string {
	style := styles.PanelStyle
	titleStyle := styles.TitleStyle
	if m.focused {
		style = styles.FocusedPanelStyle
		titleStyle = styles.FocusedTitleStyle
	}

	title := titleStyle.Render(m.Title())
	content := m.View()

	// Apply size constraints
	style = style.Width(m.width).Height(m.height)

	return lipgloss.JoinVertical(lipgloss.Left,
		title,
		style.Render(content),
	)
}
