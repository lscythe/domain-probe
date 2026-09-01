package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

// Prompt asks for the inputs when the command is run bare. It returns the raw
// name tokens, the TLD scope spec, and whether to include aftermarket listings.
func Prompt() (names []string, scope string, auction bool, err error) {
	raw := ""
	scope = "popular"
	form := huh.NewForm(huh.NewGroup(
		huh.NewInput().
			Title("Name(s) to check").
			Description("Space separated. A dotted token is checked as-is.").
			Placeholder("navigo").
			Value(&raw),
		huh.NewSelect[string]().
			Title("TLD scope").
			Options(
				huh.NewOption("Popular (~60, seconds)", "popular"),
				huh.NewOption("Every RDAP TLD (~1200, a minute)", "all"),
				huh.NewOption("Classic five (com net org io dev)", "com,net,org,io,dev"),
			).
			Value(&scope),
		huh.NewConfirm().
			Title("Also check aftermarket listings?").
			Description("Needs DYNADOT_API_KEY.").
			Value(&auction),
	))
	if err := form.Run(); err != nil {
		return nil, "", false, err
	}
	return strings.Fields(raw), scope, auction, nil
}

// --- results browser ---

// browser scrolls the same lipgloss table used for static output, so the
// interactive and piped views look identical.
type browser struct {
	all    []Result
	view   []Result
	filter textinput.Model
	typing bool
	cursor int
	offset int
	rows   int // visible data rows
	opened string
}

func newBrowser(results []Result) *browser {
	ti := textinput.New()
	ti.Prompt = "filter: "
	ti.Placeholder = "domain or status"
	return &browser{all: results, view: results, filter: ti, rows: 15}
}

func (m *browser) Init() tea.Cmd { return nil }

func (m *browser) applyFilter() {
	q := strings.ToLower(strings.TrimSpace(m.filter.Value()))
	if q == "" {
		m.view = m.all
	} else {
		m.view = m.view[:0:0]
		for _, r := range m.all {
			hay := strings.ToLower(r.Domain + " " + string(r.Status) + " " + detailOf(r))
			if strings.Contains(hay, q) {
				m.view = append(m.view, r)
			}
		}
	}
	m.cursor, m.offset = 0, 0
}

func (m *browser) move(delta int) {
	m.cursor += delta
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor > len(m.view)-1 {
		m.cursor = len(m.view) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	// Keep the cursor inside the visible window.
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+m.rows {
		m.offset = m.cursor - m.rows + 1
	}
}

func (m *browser) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// Header, filter line, table chrome and help take about 8 lines.
		if m.rows = msg.Height - 8; m.rows < 3 {
			m.rows = 3
		}
		m.move(0)
		return m, nil

	case tea.KeyMsg:
		if m.typing {
			switch msg.String() {
			case "esc":
				m.typing = false
				m.filter.SetValue("")
				m.filter.Blur()
				m.applyFilter()
				return m, nil
			case "enter":
				m.typing = false
				m.filter.Blur()
				return m, nil
			}
			var cmd tea.Cmd
			m.filter, cmd = m.filter.Update(msg)
			m.applyFilter()
			return m, cmd
		}

		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		case "/":
			m.typing = true
			m.filter.Focus()
			return m, textinput.Blink
		case "up", "k":
			m.move(-1)
		case "down", "j":
			m.move(1)
		case "pgup":
			m.move(-m.rows)
		case "pgdown", " ":
			m.move(m.rows)
		case "home", "g":
			m.move(-len(m.view))
		case "end", "G":
			m.move(len(m.view))
		case "enter":
			if m.cursor < len(m.view) {
				if u := m.view[m.cursor].Buy; u != "" {
					_ = openURL(u)
					m.opened = u
				}
			}
		}
	}
	return m, nil
}

func (m *browser) View() string {
	end := m.offset + m.rows
	if end > len(m.view) {
		end = len(m.view)
	}
	visible := m.view[m.offset:end]

	// Highlight the cursor row by index within the visible slice.
	selected := m.cursor - m.offset
	body := renderTable(visible, selected)

	head := lipgloss.NewStyle().Bold(true).Render(fmt.Sprintf("%d domains", len(m.all)))
	if len(m.view) != len(m.all) {
		head += lipgloss.NewStyle().Faint(true).Render(fmt.Sprintf("  ·  %d matching", len(m.view)))
	}
	head += "   " + summary(m.all)

	filterLine := lipgloss.NewStyle().Faint(true).Render("/ to filter")
	if m.typing || m.filter.Value() != "" {
		filterLine = m.filter.View()
	}

	help := "↑/↓ move · / filter · enter opens buy page · q quit"
	if m.opened != "" {
		help = "opened " + m.opened
	}
	if len(m.view) == 0 {
		body = lipgloss.NewStyle().Faint(true).Render("  no matches")
	}

	return strings.Join([]string{
		head,
		filterLine,
		body,
		lipgloss.NewStyle().Faint(true).Render(help),
	}, "\n")
}

// Browse shows results in a scrollable, filterable table. Enter opens the buy page.
func Browse(results []Result) error {
	_, err := tea.NewProgram(newBrowser(results), tea.WithAltScreen()).Run()
	return err
}

func isTTY(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}
