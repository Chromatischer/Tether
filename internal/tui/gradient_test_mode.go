package tui

import (
	"image/color"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/ssh"
)

type gradientTestModel struct {
	width  int
	height int
	env    map[string]string
}

func NewGradientTestModel(s ssh.Session) tea.Model {
	env := map[string]string{}
	if s != nil {
		for _, entry := range s.Environ() {
			key, value, ok := strings.Cut(entry, "=")
			if !ok {
				continue
			}
			env[key] = value
		}
	}
	return gradientTestModel{env: env}
}

func (m gradientTestModel) Init() tea.Cmd { return nil }

func (m gradientTestModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case tea.KeyPressMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m gradientTestModel) View() tea.View {
	w := max(60, m.width)
	if w <= 0 {
		w = 100
	}
	contentW := max(40, w-4)

	title := styleTitleBg.Render("gradient diagnostics")
	meta := []string{
		"TERM: " + envOrFallback(m.env, "TERM"),
		"COLORTERM: " + envOrFallback(m.env, "COLORTERM"),
		"Press q to quit.",
	}

	sections := []string{
		title,
		styleMutedBg.Render(strings.Join(meta, "  ·  ")),
		renderGradientStrip(contentW, lipgloss.Color("#b94f2b"), lipgloss.Color("#e59617"), "1D logo ramp"),
		renderGradientStrip(contentW, lipgloss.Color("#7a2e1f"), lipgloss.Color("#f0a11a"), "wide warm ramp"),
		renderGradientGrid(contentW, 8, "2D field"),
		styleLogo.Render(renderBrandLogo(tetherLogo, colorBg, true)),
		styleMutedBg.Render("If this still bands, the terminal is quantizing cell colors harder than the math."),
	}

	body := strings.Join(sections, "\n\n")
	return tea.NewView(styleBodyBg.Width(w).Render(body))
}

func envOrFallback(env map[string]string, key string) string {
	if v := strings.TrimSpace(env[key]); v != "" {
		return v
	}
	return "(unset)"
}

func renderGradientStrip(width int, start, end color.Color, label string) string {
	if width < 8 {
		width = 8
	}
	gradient := lipgloss.Blend1D(width, start, end)
	var b strings.Builder
	for i := 0; i < width; i++ {
		b.WriteString(lipgloss.NewStyle().Background(gradient[i]).Render(" "))
	}
	return styleAccentBg.Render(label) + "\n" + b.String()
}

func renderGradientGrid(width int, height int, label string) string {
	if width < 8 {
		width = 8
	}
	if height < 2 {
		height = 2
	}
	gradient := lipgloss.Blend2D(width, height, 18, lipgloss.Color("#b94f2b"), lipgloss.Color("#e59617"))
	var b strings.Builder
	for y := 0; y < height; y++ {
		if y > 0 {
			b.WriteRune('\n')
		}
		for x := 0; x < width; x++ {
			b.WriteString(lipgloss.NewStyle().Background(gradient[y*width+x]).Render(" "))
		}
	}
	return styleAccentBg.Render(label) + "\n" + b.String()
}
