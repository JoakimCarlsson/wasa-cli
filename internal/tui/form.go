package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/joakimcarlsson/wasa/internal/launch"
	"github.com/joakimcarlsson/wasa/internal/worktree"
)

// Create-form fields, in tab order: the directory comes first and the branch
// sits under it, since picking where a session runs is the common case and a
// branch (a worktree session) is the opt-in. The profile selector is the last
// field and is not a text input; the rest are.
const (
	fieldDir = iota
	fieldBranch
	fieldTitle
	fieldProgram
	fieldAutonomous
	fieldProfile
	fieldCount
)

// formResult is what a form update reports back to the parent model.
type formResult int

const (
	formNone formResult = iota
	formSubmit
	formCancel
	formPickDir
	formPickBranch
)

// createForm collects the inputs for a new session. The two session shapes share
// one form: leaving Branch empty creates a plain session that runs in the
// Directory field; entering a branch opts into a worktree session created on it.
// The Branch field is only meaningful when the chosen Directory resolves to a git
// repository, since a worktree is created against that repository; so it is
// enabled only when branchRepo is set, and disabled (skipped in tab order and
// shown dimmed) otherwise. branchRepo is the repository toplevel resolved from the
// Directory field — re-derived whenever that field changes. An empty Directory has
// no branch context, so the field is disabled until a directory is chosen. Title
// and program are optional, and a profile is chosen from the workspace's profiles
// with the default (first) preselected. The program field shows every agent
// detected on PATH plus a bare-shell entry as a visible menu; ←/→ move the
// selection and typing overrides it with any program name outside the known set.
type createForm struct {
	inputs     []textinput.Model
	branchRepo string
	profiles   []string
	profIdx    int
	programs   []string
	shell      string
	progIdx    int
	autonomous bool
	focus      int
	err        string
}

func newCreateForm(profiles []string) createForm {
	dir := textinput.New()
	dir.Placeholder = "ctrl+f to browse, or empty for here"
	dir.CharLimit = 4096
	dir.Focus()

	branch := textinput.New()
	branch.Placeholder = "ctrl+f to pick a branch (worktree session)"
	branch.CharLimit = 200

	title := textinput.New()
	title.Placeholder = "optional title"
	title.CharLimit = 200

	shell := launch.Shell()
	programs := append(launch.DetectAgents(), shell)

	program := textinput.New()
	program.CharLimit = 200
	program.SetValue(programs[0])

	f := createForm{
		inputs:   []textinput.Model{dir, branch, title, program},
		profiles: profiles,
		programs: programs,
		shell:    shell,
	}
	f.syncBranchRepo()
	return f
}

// syncBranchRepo re-derives the repository the Branch field operates on from the
// Directory field's current value, so the field's enabled state and the branch
// picker always reflect the directory as currently chosen. An empty Directory has
// no branch context and disables the field; a Directory inside a git repository
// resolves to that repository; anything else (a plain directory, a path that does
// not exist, git absent) leaves it empty, disabling the field.
func (f *createForm) syncBranchRepo() {
	f.branchRepo = branchRepoFor(f.dir())
}

// branchRepoFor resolves the repository toplevel that should back the Branch
// field for dir. It returns an empty string when dir is empty or not inside a git
// repository, mirroring repoBranches in swallowing resolution failures rather than
// surfacing them.
func branchRepoFor(dir string) string {
	if dir == "" {
		return ""
	}
	top, err := worktree.Toplevel(dir)
	if err != nil {
		return ""
	}
	return top
}

func (f createForm) update(msg tea.Msg) (createForm, formResult, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "esc":
			return f, formCancel, nil
		case "ctrl+f":
			switch f.focus {
			case fieldDir:
				return f, formPickDir, nil
			case fieldBranch:
				if f.branchEnabled() {
					return f, formPickBranch, nil
				}
			}
			return f, formNone, nil
		case "enter":
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
			case fieldAutonomous:
				f.toggleAutonomous()
				return f, formNone, nil
			}
		case " ":
			if f.focus == fieldAutonomous {
				f.toggleAutonomous()
				return f, formNone, nil
			}
		}
	}

	if f.focus < len(f.inputs) {
		var cmd tea.Cmd
		f.inputs[f.focus], cmd = f.inputs[f.focus].Update(msg)
		if f.focus == fieldDir {
			f.syncBranchRepo()
		}
		return f, formNone, cmd
	}
	return f, formNone, nil
}

// setProfiles replaces the profile menu with names, the profiles of the
// workspace the form's current Directory resolves to. It preserves the user's
// selection by name when that profile still exists, and otherwise falls back to
// the default (first) profile, so switching to a directory in a different
// repository never leaves a profile selected that is invalid there.
func (f *createForm) setProfiles(names []string) {
	cur := ""
	if f.profIdx < len(f.profiles) {
		cur = f.profiles[f.profIdx]
	}
	f.profiles = names
	f.profIdx = 0
	for i, n := range names {
		if n == cur {
			f.profIdx = i
			break
		}
	}
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

// toggleAutonomous flips the autonomous (skip-permissions) toggle, but only when
// the selected program has a known autonomous flag — there is nothing to enable
// for a shell or an unknown program.
func (f *createForm) toggleAutonomous() {
	if !f.autonomousEnabled() {
		return
	}
	f.autonomous = !f.autonomous
}

// autonomousEnabled reports whether the autonomous toggle is usable: only when
// the program as currently typed maps to a known skip-permissions flag. It reads
// the live Program field so a free-typed agent name is honoured too.
func (f createForm) autonomousEnabled() bool {
	return launch.AutonomousAvailable(
		strings.TrimSpace(f.inputs[fieldProgram].Value()),
	)
}

// dir is the Directory field's current value, trimmed. It seeds the directory
// browser's starting point when the picker is opened.
func (f createForm) dir() string {
	return strings.TrimSpace(f.inputs[fieldDir].Value())
}

// setDir writes a path chosen in the directory picker into the Directory field
// and moves focus to it, so the picked value is visible and editable on return.
func (f *createForm) setDir(path string) {
	f.inputs[fieldDir].SetValue(path)
	f.syncBranchRepo()
	f.setFocus(fieldDir)
}

// setBranch writes a branch chosen or typed in the branch picker into the Branch
// field and moves focus to it.
func (f *createForm) setBranch(branch string) {
	f.inputs[fieldBranch].SetValue(branch)
	f.setFocus(fieldBranch)
}

// branchEnabled reports whether the Branch field is usable: only when the chosen
// Directory resolves to a git repository (or, with an empty Directory, when wasa
// was launched inside one), since a worktree session is created against that
// repository. branchRepo is kept in sync with the Directory field, so this reads
// the cached resolution rather than shelling out to git on every render.
func (f createForm) branchEnabled() bool {
	return f.branchRepo != ""
}

func (f *createForm) focusNext() { f.setFocus(f.stepFocus(1)) }

func (f *createForm) focusPrev() { f.setFocus(f.stepFocus(-1)) }

// stepFocus returns the next focusable field in the given direction, skipping
// the Branch and Autonomous fields when they are disabled so tab never lands on a
// dead input.
func (f createForm) stepFocus(dir int) int {
	i := f.focus
	for range fieldCount {
		i = (i + dir + fieldCount) % fieldCount
		if i == fieldBranch && !f.branchEnabled() {
			continue
		}
		if i == fieldAutonomous && !f.autonomousEnabled() {
			continue
		}
		return i
	}
	return f.focus
}

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
	program := strings.TrimSpace(f.inputs[fieldProgram].Value())
	if f.autonomous && f.autonomousEnabled() {
		program = launch.WithAutonomous(program)
	}
	p := launch.Params{
		Title:   strings.TrimSpace(f.inputs[fieldTitle].Value()),
		Program: program,
		Profile: prof,
	}
	branch := ""
	if f.branchEnabled() {
		branch = strings.TrimSpace(f.inputs[fieldBranch].Value())
	}
	if branch != "" {
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

	labels := []string{"Directory", "Branch", "Title"}
	for i := fieldDir; i <= fieldTitle; i++ {
		b.WriteString(f.label(labels[i], i))
		b.WriteString("\n")
		if i == fieldBranch && !f.branchEnabled() {
			b.WriteString(dimStyle.Render("  (only inside a git repository)"))
		} else {
			b.WriteString(f.inputs[i].View())
		}
		b.WriteString("\n\n")
	}

	b.WriteString(f.label("Program", fieldProgram))
	b.WriteString("\n")
	b.WriteString(f.programView())
	b.WriteString("\n\n")

	b.WriteString(f.label("Autonomous", fieldAutonomous))
	b.WriteString("\n")
	b.WriteString(f.autonomousView())
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
		"tab/↑↓ move · ←/→/space choose/toggle · " +
			"ctrl+f browse dir/branch · enter create · esc cancel",
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

// autonomousView renders the skip-permissions toggle: a checkbox with an inline
// hint about the consequence when the selected program supports it, or a dim
// note that it is unavailable for a shell or unknown program.
func (f createForm) autonomousView() string {
	if !f.autonomousEnabled() {
		return dimStyle.Render("  (not available for this program)")
	}
	box := "[ ]"
	if f.autonomous {
		box = "[x]"
	}
	hint := dimStyle.Render(" runs without approval prompts")
	return "  " + box + " skip permissions" + hint
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
