package modal

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/joakimcarlsson/wasa-cli/internal/launch"
	"github.com/joakimcarlsson/wasa-cli/internal/tui/theme"
	"github.com/joakimcarlsson/wasa-cli/internal/worktree"
)

// Create-form fields, in tab order: the directory comes first and the branch
// sits under it, since picking where a session runs is the common case and a
// branch (a worktree session) is the opt-in. The profile selector is the last
// field and is not a text input; the rest are.
const (
	fieldDir = iota
	fieldBranch
	fieldTitle
	fieldPrompt
	fieldProgram
	fieldAutonomous
	fieldProfile
	fieldCount
)

// FormSubmitMsg is emitted when the user accepts the create form; the parent
// builds the session.
type FormSubmitMsg struct{}

// FormCancelMsg is emitted when the user dismisses the create form.
type FormCancelMsg struct{}

// FormPickDirMsg is emitted when the user opens the directory browser over the
// form.
type FormPickDirMsg struct{}

// FormPickBranchMsg is emitted when the user opens the branch picker over the
// form.
type FormPickBranchMsg struct{}

func formSubmit() tea.Msg { return FormSubmitMsg{} }

func formCancel() tea.Msg { return FormCancelMsg{} }

func formPickDir() tea.Msg { return FormPickDirMsg{} }

func formPickBranch() tea.Msg { return FormPickBranchMsg{} }

// CreateForm collects the inputs for a new session. The two session shapes share
// one form: leaving Branch empty creates a plain session that runs in a working
// directory; entering a branch opts into a worktree session created on it.
//
// The form has two modes, set at construction. Inside a workspace WorkspaceRepo
// holds that workspace's repository root: the Directory field is dropped (a
// session created in a workspace is anchored to its repo, never a free-form path),
// the working directory of a plain session is the repo root, and a worktree
// session's branch resolves against that same repo. Outside any workspace
// WorkspaceRepo is empty: the Directory field returns, and the path picked there
// is the session's only anchor — it both sets a plain session's working directory
// and resolves the repository a worktree session's branch operates on.
//
// The Branch field is only meaningful when a repository backs it, so it is enabled
// only when BranchRepo is set, and disabled (skipped in tab order and shown
// dimmed) otherwise. BranchRepo is that repository toplevel: the fixed
// WorkspaceRepo in workspace mode, or the toplevel re-derived from the Directory
// field in orphan mode (empty until a directory inside a git repository is
// chosen). Title and program are optional, and a profile is chosen from the
// workspace's profiles with the default (first) preselected. The program field
// shows every agent detected on PATH plus a bare-shell entry as a visible menu;
// ←/→ move the selection and typing overrides it with any program name outside the
// known set.
type CreateForm struct {
	theme         theme.Theme
	inputs        []textinput.Model
	BranchRepo    string
	WorkspaceRepo string
	profiles      []string
	profIdx       int
	programs      []string
	shell         string
	progIdx       int
	autonomous    bool
	focus         int
	err           string
}

// NewCreateForm builds the create form for a workspace's profiles, styled with
// theme. workspaceRepo is the active workspace's repository root, or "" when wasa
// is creating a session outside any workspace (the orphan/scratch case). A
// non-empty workspaceRepo drops the Directory field and anchors the session to
// that repo; an empty one restores the Directory field, which starts focused and
// empty, as the session's only anchor.
// formInputWidth is the visible width given to every text input in the create
// form. Bubbles v2 clips a textinput's placeholder (and value) to its
// configured width, collapsing to a single character when unset, so each field
// needs an explicit width wide enough to show its longest placeholder in full.
const formInputWidth = 50

func NewCreateForm(
	theme theme.Theme,
	profiles []string,
	workspaceRepo string,
) CreateForm {
	dir := textinput.New()
	dir.Placeholder = "ctrl+f to browse, or empty for here"
	dir.CharLimit = 4096
	dir.SetWidth(formInputWidth)

	branch := textinput.New()
	branch.Placeholder = "ctrl+f to pick a branch (worktree session)"
	branch.CharLimit = 200
	branch.SetWidth(formInputWidth)

	title := textinput.New()
	title.Placeholder = "optional title"
	title.CharLimit = 200
	title.SetWidth(formInputWidth)

	prompt := textinput.New()
	prompt.Placeholder = "optional — what this session should do"
	prompt.CharLimit = 4096
	prompt.SetWidth(formInputWidth)

	shell := launch.Shell()
	programs := append(launch.DetectAgents(), shell)

	program := textinput.New()
	program.CharLimit = 200
	program.SetValue(programs[0])

	f := CreateForm{
		theme:         theme,
		inputs:        []textinput.Model{dir, branch, title, prompt, program},
		profiles:      profiles,
		programs:      programs,
		shell:         shell,
		WorkspaceRepo: workspaceRepo,
	}
	f.SyncBranchRepo()
	f.setFocus(f.firstField())
	return f
}

// dirEnabled reports whether the Directory field is present: only in orphan mode
// (no active workspace), where the picked path is the session's anchor. Inside a
// workspace the field is dropped, since the session is anchored to the workspace's
// repository.
func (f CreateForm) dirEnabled() bool {
	return f.WorkspaceRepo == ""
}

// firstField is the field focused when the form opens: the Directory field in
// orphan mode, or the Branch field when the Directory field has been dropped
// inside a workspace.
func (f CreateForm) firstField() int {
	if f.dirEnabled() {
		return fieldDir
	}
	return fieldBranch
}

// SyncBranchRepo re-derives the repository the Branch field operates on. In
// workspace mode it is fixed to the workspace's repository, so a worktree session
// always lands in the workspace's repo. In orphan mode it follows the Directory
// field's current value, so the field's enabled state and the branch picker
// reflect the directory as currently chosen: an empty Directory has no branch
// context and disables the field; a Directory inside a git repository resolves to
// that repository; anything else (a plain directory, a path that does not exist,
// git absent) leaves it empty, disabling the field.
func (f *CreateForm) SyncBranchRepo() {
	if f.WorkspaceRepo != "" {
		f.BranchRepo = f.WorkspaceRepo
		return
	}
	f.BranchRepo = branchRepoFor(f.Dir())
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

// Update routes a message into the form, returning the updated form and a
// command that emits a FormSubmitMsg, FormCancelMsg, FormPickDirMsg or
// FormPickBranchMsg on the key that triggers it, or nil otherwise.
func (f CreateForm) Update(msg tea.Msg) (CreateForm, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.String() {
		case "esc":
			return f, formCancel
		case "ctrl+f":
			switch f.focus {
			case fieldDir:
				return f, formPickDir
			case fieldBranch:
				if f.BranchEnabled() {
					return f, formPickBranch
				}
			}
			return f, nil
		case "enter":
			return f, formSubmit
		case "tab", "down":
			f.focusNext()
			return f, nil
		case "shift+tab", "up":
			f.focusPrev()
			return f, nil
		case "left", "right":
			switch f.focus {
			case fieldProfile:
				f.cycleProfile(key.String() == "right")
				return f, nil
			case fieldProgram:
				f.cycleProgram(key.String() == "right")
				return f, nil
			case fieldAutonomous:
				f.toggleAutonomous()
				return f, nil
			}
		case "space":
			if f.focus == fieldAutonomous {
				f.toggleAutonomous()
				return f, nil
			}
		}
	}

	if f.focus < len(f.inputs) {
		var cmd tea.Cmd
		f.inputs[f.focus], cmd = f.inputs[f.focus].Update(msg)
		if f.focus == fieldDir {
			f.SyncBranchRepo()
		}
		return f, cmd
	}
	return f, nil
}

// SetProfiles replaces the profile menu with names, the profiles of the
// workspace the form's current Directory resolves to. It preserves the user's
// selection by name when that profile still exists, and otherwise falls back to
// the default (first) profile, so switching to a directory in a different
// repository never leaves a profile selected that is invalid there.
func (f *CreateForm) SetProfiles(names []string) {
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

func (f *CreateForm) cycleProfile(forward bool) {
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
func (f *CreateForm) cycleProgram(forward bool) {
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
func (f *CreateForm) toggleAutonomous() {
	if !f.autonomousEnabled() {
		return
	}
	f.autonomous = !f.autonomous
}

// autonomousEnabled reports whether the autonomous toggle is usable: only when
// the program as currently typed maps to a known skip-permissions flag. It reads
// the live Program field so a free-typed agent name is honoured too.
func (f CreateForm) autonomousEnabled() bool {
	return launch.AutonomousAvailable(
		strings.TrimSpace(f.inputs[fieldProgram].Value()),
	)
}

// Dir is the Directory field's current value, trimmed. It seeds the directory
// browser's starting point when the picker is opened.
func (f CreateForm) Dir() string {
	return strings.TrimSpace(f.inputs[fieldDir].Value())
}

// SetDir writes a path chosen in the directory picker into the Directory field
// and moves focus to it, so the picked value is visible and editable on return.
func (f *CreateForm) SetDir(path string) {
	f.inputs[fieldDir].SetValue(path)
	f.SyncBranchRepo()
	f.setFocus(fieldDir)
}

// SetBranch writes a branch chosen or typed in the branch picker into the Branch
// field and moves focus to it.
func (f *CreateForm) SetBranch(branch string) {
	f.inputs[fieldBranch].SetValue(branch)
	f.setFocus(fieldBranch)
}

// BranchEnabled reports whether the Branch field is usable: only when the chosen
// Directory resolves to a git repository (or, with an empty Directory, when wasa
// was launched inside one), since a worktree session is created against that
// repository. BranchRepo is kept in sync with the Directory field, so this reads
// the cached resolution rather than shelling out to git on every render.
func (f CreateForm) BranchEnabled() bool {
	return f.BranchRepo != ""
}

func (f *CreateForm) focusNext() { f.setFocus(f.stepFocus(1)) }

func (f *CreateForm) focusPrev() { f.setFocus(f.stepFocus(-1)) }

// stepFocus returns the next focusable field in the given direction, skipping
// the Directory field when it has been dropped inside a workspace and the Branch
// and Autonomous fields when they are disabled, so tab never lands on a dead or
// absent input.
func (f CreateForm) stepFocus(dir int) int {
	i := f.focus
	for range fieldCount {
		i = (i + dir + fieldCount) % fieldCount
		if i == fieldDir && !f.dirEnabled() {
			continue
		}
		if i == fieldBranch && !f.BranchEnabled() {
			continue
		}
		if i == fieldAutonomous && !f.autonomousEnabled() {
			continue
		}
		return i
	}
	return f.focus
}

func (f *CreateForm) setFocus(i int) {
	f.focus = i
	for j := range f.inputs {
		if j == i {
			f.inputs[j].Focus()
		} else {
			f.inputs[j].Blur()
		}
	}
}

// Params reads the form into launch.Params. A non-empty branch selects a
// worktree session and the working directory is ignored. An empty branch selects
// a plain session, run in the workspace's repository root inside a workspace, or
// in the Directory field's value in orphan mode.
func (f CreateForm) Params() launch.Params {
	prof := ""
	if f.profIdx < len(f.profiles) {
		prof = f.profiles[f.profIdx]
	}
	program := strings.TrimSpace(f.inputs[fieldProgram].Value())
	if f.autonomous && f.autonomousEnabled() {
		program = launch.WithAutonomous(program)
	}
	p := launch.Params{
		Title:         strings.TrimSpace(f.inputs[fieldTitle].Value()),
		Program:       program,
		Profile:       prof,
		InitialPrompt: strings.TrimSpace(f.inputs[fieldPrompt].Value()),
	}
	branch := ""
	if f.BranchEnabled() {
		branch = strings.TrimSpace(f.inputs[fieldBranch].Value())
	}
	switch {
	case branch != "":
		p.Branch = branch
	case f.WorkspaceRepo != "":
		p.WorkingDir = f.WorkspaceRepo
	default:
		p.WorkingDir = strings.TrimSpace(f.inputs[fieldDir].Value())
	}
	return p
}

// View renders the create form.
func (f CreateForm) View() string {
	var b strings.Builder
	b.WriteString(f.theme.TitleStyle.Render("New session"))
	b.WriteString("\n\n")

	labels := []string{"Directory", "Branch", "Title", "Prompt"}
	for i := fieldDir; i <= fieldPrompt; i++ {
		if i == fieldDir && !f.dirEnabled() {
			continue
		}
		b.WriteString(f.label(labels[i], i))
		b.WriteString("\n")
		if i == fieldBranch && !f.BranchEnabled() {
			b.WriteString(
				f.theme.DimStyle.Render("  (only inside a git repository)"),
			)
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
		b.WriteString(f.theme.ErrorStyle.Render(f.err))
		b.WriteString("\n\n")
	}
	browse := "ctrl+f pick branch"
	if f.dirEnabled() {
		browse = "ctrl+f browse dir/branch"
	}
	b.WriteString(f.theme.DimStyle.Render(
		"tab/↑↓ move · ←/→/space choose/toggle · " +
			browse + " · enter create · esc cancel",
	))
	return b.String()
}

func (f CreateForm) label(text string, field int) string {
	if f.focus == field {
		return f.theme.FocusedLabelStyle.Render("> " + text)
	}
	return f.theme.LabelStyle.Render("  " + text)
}

// programView renders the detected-agents-plus-shell menu inline, highlighting
// the active entry. A value typed outside the known set is shown as a trailing
// highlighted entry so free-text overrides stay visible.
func (f CreateForm) programView() string {
	cur := strings.TrimSpace(f.inputs[fieldProgram].Value())
	parts := make([]string, 0, len(f.programs)+1)
	matched := false
	for _, p := range f.programs {
		if p == cur {
			matched = true
			parts = append(
				parts,
				f.theme.FocusedLabelStyle.Render("["+f.programLabel(p)+"]"),
			)
			continue
		}
		parts = append(parts, f.theme.DimStyle.Render(f.programLabel(p)))
	}
	if !matched && cur != "" {
		parts = append(parts, f.theme.FocusedLabelStyle.Render("["+cur+"]"))
	}
	return "  " + strings.Join(parts, "   ")
}

// programLabel is the menu label for a program value: the shell entry shows as
// "shell" rather than its resolved path, every agent shows by name.
func (f CreateForm) programLabel(p string) string {
	if p == f.shell {
		return "shell"
	}
	return p
}

// autonomousView renders the skip-permissions toggle: a checkbox with an inline
// hint about the consequence when the selected program supports it, or a dim
// note that it is unavailable for a shell or unknown program.
func (f CreateForm) autonomousView() string {
	if !f.autonomousEnabled() {
		return f.theme.DimStyle.Render("  (not available for this program)")
	}
	box := "[ ]"
	if f.autonomous {
		box = "[x]"
	}
	hint := f.theme.DimStyle.Render(" runs without approval prompts")
	return "  " + box + " skip permissions" + hint
}

func (f CreateForm) profileView() string {
	if len(f.profiles) == 0 {
		return f.theme.DimStyle.Render("  (no profiles)")
	}
	name := f.profiles[f.profIdx]
	marker := ""
	if f.profIdx == 0 {
		marker = " (default)"
	}
	return fmt.Sprintf("  ◄ %s%s ►", name, f.theme.DimStyle.Render(marker))
}
