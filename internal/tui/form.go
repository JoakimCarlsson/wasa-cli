package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/joakimcarlsson/wasa/internal/launch"
)

// Create-form fields, in tab order. The profile selector is the last field and
// is not a text input; the rest are.
const (
	fieldBranch = iota
	fieldTitle
	fieldProgram
	fieldProfile
	fieldCount
)

// formResult is what a form update reports back to the parent model.
type formResult int

const (
	formNone formResult = iota
	formSubmit
	formCancel
)

// createForm collects the inputs for a new session: branch, optional title,
// program, and a profile chosen from the workspace's profiles with the default
// (first) preselected.
type createForm struct {
	inputs   []textinput.Model
	profiles []string
	profIdx  int
	focus    int
	err      string
}

func newCreateForm(profiles []string) createForm {
	branch := textinput.New()
	branch.Placeholder = "feature/my-branch"
	branch.CharLimit = 200
	branch.Focus()

	title := textinput.New()
	title.Placeholder = "optional title"
	title.CharLimit = 200

	program := textinput.New()
	program.CharLimit = 200
	program.SetValue(launch.DefaultProgram)

	return createForm{
		inputs:   []textinput.Model{branch, title, program},
		profiles: profiles,
	}
}

func (f createForm) update(msg tea.Msg) (createForm, formResult, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "esc":
			return f, formCancel, nil
		case "enter":
			if strings.TrimSpace(f.inputs[fieldBranch].Value()) == "" {
				f.err = "branch is required"
				return f, formNone, nil
			}
			return f, formSubmit, nil
		case "tab", "down":
			f.focusNext()
			return f, formNone, nil
		case "shift+tab", "up":
			f.focusPrev()
			return f, formNone, nil
		case "left", "right":
			if f.focus == fieldProfile {
				f.cycleProfile(key.String() == "right")
				return f, formNone, nil
			}
		}
	}

	if f.focus < len(f.inputs) {
		var cmd tea.Cmd
		f.inputs[f.focus], cmd = f.inputs[f.focus].Update(msg)
		return f, formNone, cmd
	}
	return f, formNone, nil
}

func (f *createForm) cycleProfile(forward bool) {
	if len(f.profiles) == 0 {
		return
	}
	if forward {
		f.profIdx = (f.profIdx + 1) % len(f.profiles)
		return
	}
	f.profIdx = (f.profIdx - 1 + len(f.profiles)) % len(f.profiles)
}

func (f *createForm) focusNext() { f.setFocus((f.focus + 1) % fieldCount) }

func (f *createForm) focusPrev() { f.setFocus((f.focus - 1 + fieldCount) % fieldCount) }

func (f *createForm) setFocus(i int) {
	f.focus = i
	for j := range f.inputs {
		if j == i {
			f.inputs[j].Focus()
		} else {
			f.inputs[j].Blur()
		}
	}
}

func (f createForm) params() launch.Params {
	prof := ""
	if f.profIdx < len(f.profiles) {
		prof = f.profiles[f.profIdx]
	}
	return launch.Params{
		Branch:  strings.TrimSpace(f.inputs[fieldBranch].Value()),
		Title:   strings.TrimSpace(f.inputs[fieldTitle].Value()),
		Program: strings.TrimSpace(f.inputs[fieldProgram].Value()),
		Profile: prof,
	}
}

func (f createForm) view() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("New session"))
	b.WriteString("\n\n")

	labels := []string{"Branch", "Title", "Program"}
	for i, in := range f.inputs {
		b.WriteString(f.label(labels[i], i))
		b.WriteString("\n")
		b.WriteString(in.View())
		b.WriteString("\n\n")
	}

	b.WriteString(f.label("Profile", fieldProfile))
	b.WriteString("\n")
	b.WriteString(f.profileView())
	b.WriteString("\n\n")

	if f.err != "" {
		b.WriteString(errorStyle.Render(f.err))
		b.WriteString("\n\n")
	}
	b.WriteString(helpStyle.Render(
		"tab/↑↓ move · ←/→ choose profile · enter create · esc cancel",
	))
	return b.String()
}

func (f createForm) label(text string, field int) string {
	if f.focus == field {
		return focusedLabelStyle.Render("> " + text)
	}
	return labelStyle.Render("  " + text)
}

func (f createForm) profileView() string {
	if len(f.profiles) == 0 {
		return dimStyle.Render("  (no profiles)")
	}
	name := f.profiles[f.profIdx]
	marker := ""
	if f.profIdx == 0 {
		marker = " (default)"
	}
	return fmt.Sprintf("  ◄ %s%s ►", name, dimStyle.Render(marker))
}
