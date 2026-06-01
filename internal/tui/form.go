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
	fieldDir
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

// createForm collects the inputs for a new session. The two session shapes share
// one form: leaving Branch empty creates a plain session that runs in the
// Directory field; entering a branch opts into a worktree session created on it.
// Title and program are optional, and a profile is chosen from the workspace's
// profiles with the default (first) preselected. The program field shows every
// agent detected on PATH plus a bare-shell entry as a visible menu; ←/→ move the
// selection and typing overrides it with any program name outside the known set.
type createForm struct {
	inputs   []textinput.Model
	profiles []string
	profIdx  int
	programs []string
	shell    string
	progIdx  int
	focus    int
	err      string
}

func newCreateForm(profiles []string, defaultDir string) createForm {
	branch := textinput.New()
	branch.Placeholder = "empty for a plain session"
	branch.CharLimit = 200
	branch.Focus()

	dir := textinput.New()
	dir.Placeholder = "working directory"
	dir.CharLimit = 4096
	dir.SetValue(defaultDir)

	title := textinput.New()
	title.Placeholder = "optional title"
	title.CharLimit = 200

	shell := launch.Shell()
	programs := append(launch.DetectAgents(), shell)

	program := textinput.New()
	program.CharLimit = 200
	program.SetValue(programs[0])

	return createForm{
		inputs:   []textinput.Model{branch, dir, title, program},
		profiles: profiles,
		programs: programs,
		shell:    shell,
	}
}

func (f createForm) update(msg tea.Msg) (createForm, formResult, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "esc":
			return f, formCancel, nil
		case "enter":
			branch := strings.TrimSpace(f.inputs[fieldBranch].Value())
			dir := strings.TrimSpace(f.inputs[fieldDir].Value())
			if branch == "" && dir == "" {
				f.err = "enter a branch for a worktree session " +
					"or a directory for a plain session"
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
			switch f.focus {
			case fieldProfile:
				f.cycleProfile(key.String() == "right")
				return f, formNone, nil
			case fieldProgram:
				f.cycleProgram(key.String() == "right")
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

// cycleProgram steps the program field through the detected-agents-plus-shell
// menu, writing the chosen program into the text input. Typing afterwards
// overrides the selection, so a program outside the known set stays reachable.
func (f *createForm) cycleProgram(forward bool) {
	if len(f.programs) == 0 {
		return
	}
	if forward {
		f.progIdx = (f.progIdx + 1) % len(f.programs)
	} else {
		f.progIdx = (f.progIdx - 1 + len(f.programs)) % len(f.programs)
	}
	f.inputs[fieldProgram].SetValue(f.programs[f.progIdx])
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

// params reads the form into launch.Params. A non-empty branch selects a
// worktree session and the directory field is ignored; an empty branch selects a
// plain session run in the directory field.
func (f createForm) params() launch.Params {
	prof := ""
	if f.profIdx < len(f.profiles) {
		prof = f.profiles[f.profIdx]
	}
	p := launch.Params{
		Title:   strings.TrimSpace(f.inputs[fieldTitle].Value()),
		Program: strings.TrimSpace(f.inputs[fieldProgram].Value()),
		Profile: prof,
	}
	if branch := strings.TrimSpace(
		f.inputs[fieldBranch].Value(),
	); branch != "" {
		p.Branch = branch
	} else {
		p.WorkingDir = strings.TrimSpace(f.inputs[fieldDir].Value())
	}
	return p
}

func (f createForm) view() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("New session"))
	b.WriteString("\n\n")

	labels := []string{"Branch", "Directory", "Title"}
	for i := fieldBranch; i <= fieldTitle; i++ {
		b.WriteString(f.label(labels[i], i))
		b.WriteString("\n")
		b.WriteString(f.inputs[i].View())
		b.WriteString("\n\n")
	}

	b.WriteString(f.label("Program", fieldProgram))
	b.WriteString("\n")
	b.WriteString(f.programView())
	b.WriteString("\n\n")

	b.WriteString(f.label("Profile", fieldProfile))
	b.WriteString("\n")
	b.WriteString(f.profileView())
	b.WriteString("\n\n")

	if f.err != "" {
		b.WriteString(errorStyle.Render(f.err))
		b.WriteString("\n\n")
	}
	b.WriteString(dimStyle.Render(
		"tab/↑↓ move · ←/→ choose program/profile · enter create · esc cancel",
	))
	return b.String()
}

func (f createForm) label(text string, field int) string {
	if f.focus == field {
		return focusedLabelStyle.Render("> " + text)
	}
	return labelStyle.Render("  " + text)
}

// programView renders the detected-agents-plus-shell menu inline, highlighting
// the active entry. A value typed outside the known set is shown as a trailing
// highlighted entry so free-text overrides stay visible.
func (f createForm) programView() string {
	cur := strings.TrimSpace(f.inputs[fieldProgram].Value())
	parts := make([]string, 0, len(f.programs)+1)
	matched := false
	for _, p := range f.programs {
		if p == cur {
			matched = true
			parts = append(
				parts,
				focusedLabelStyle.Render("["+f.programLabel(p)+"]"),
			)
			continue
		}
		parts = append(parts, dimStyle.Render(f.programLabel(p)))
	}
	if !matched && cur != "" {
		parts = append(parts, focusedLabelStyle.Render("["+cur+"]"))
	}
	return "  " + strings.Join(parts, "   ")
}

// programLabel is the menu label for a program value: the shell entry shows as
// "shell" rather than its resolved path, every agent shows by name.
func (f createForm) programLabel(p string) string {
	if p == f.shell {
		return "shell"
	}
	return p
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
