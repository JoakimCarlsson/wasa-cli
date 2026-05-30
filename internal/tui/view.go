package tui

import (
	"fmt"
	"strings"

	"github.com/joakimcarlsson/wasa/internal/registry"
)

// View implements tea.Model.
func (m Model) View() string {
	if m.mode == modeCreate {
		return m.form.view() + m.footer()
	}

	if len(m.workspaces) == 0 {
		return m.chrome(noWorkspaceBanner())
	}

	var b strings.Builder
	b.WriteString(m.tabBar())
	b.WriteString("\n\n")
	b.WriteString(m.sessionList())
	return m.chrome(b.String())
}

func (m Model) chrome(body string) string {
	return body + m.footer()
}

func (m Model) tabBar() string {
	parts := make([]string, len(m.workspaces))
	active := m.tabIndex()
	for i, w := range m.workspaces {
		if i == active {
			parts[i] = activeTabStyle.Render(w.Name)
		} else {
			parts[i] = inactiveTabStyle.Render(w.Name)
		}
	}
	return strings.Join(parts, " ")
}

func (m Model) sessionList() string {
	ss := m.sessions()
	if len(ss) == 0 {
		ws := m.currentWorkspace()
		name := ""
		if ws != nil {
			name = ws.Name
		}
		return noSessionBanner(name)
	}

	var b strings.Builder
	for i, s := range ss {
		b.WriteString(m.sessionRow(i, s))
		b.WriteString("\n")
	}
	return b.String()
}

func (m Model) sessionRow(i int, s *registry.Session) string {
	marker := "  "
	if i == m.cursor {
		marker = selectedRowStyle.Render("> ")
	}

	title := s.Title
	if title == "" {
		title = s.Branch
	}

	row := fmt.Sprintf(
		"%s %-24s %s",
		statusDot(s.Status),
		title,
		dimStyle.Render(fmt.Sprintf("%s · %s", s.Branch, s.ProfileName)),
	)
	if i == m.cursor {
		row = selectedRowStyle.Render(row)
	}
	return marker + row
}

func (m Model) footer() string {
	var b strings.Builder
	b.WriteString("\n")
	if m.err != nil {
		b.WriteString(errorStyle.Render("error: " + m.err.Error()))
		b.WriteString("\n")
	} else if m.status != "" {
		b.WriteString(dimStyle.Render(m.status))
		b.WriteString("\n")
	}
	if m.mode == modeList {
		b.WriteString(helpStyle.Render(
			"n new · enter attach · k kill · ←/→ tabs · ↑/↓ select · q quit",
		))
	}
	return b.String()
}

func statusDot(status string) string {
	if status == registry.StatusRunning {
		return runningDotStyle.Render("●")
	}
	return exitedDotStyle.Render("●")
}

func noWorkspaceBanner() string {
	return bannerStyle.Render("No workspaces yet.") + "\n\n" +
		dimStyle.Render(
			"Run wasa inside a git repository to register it as a workspace,\n"+
				"then press n to create your first session.",
		) + "\n"
}

func noSessionBanner(name string) string {
	title := "No sessions in this workspace."
	if name != "" {
		title = fmt.Sprintf("No sessions in %s.", name)
	}
	return bannerStyle.Render(title) + "\n\n" +
		dimStyle.Render("Press n to create one.")
}
