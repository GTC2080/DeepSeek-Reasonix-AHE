package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"reasonix/internal/i18n"
	"reasonix/internal/skill"
)

// scopePriority orders skills by usefulness: project-level skills first,
// then custom paths, global, and builtins last. Within each tier,
// alphabetical order keeps the list scannable.
var scopePriority = map[skill.Scope]int{
	skill.ScopeProject: 0,
	skill.ScopeCustom:  1,
	skill.ScopeGlobal:  2,
	skill.ScopeBuiltin: 3,
}

type skillPickerMode string

const (
	pickerSkills        skillPickerMode = "skills"
	pickerSources       skillPickerMode = "sources"
	pickerSourceSkills  skillPickerMode = "source-skills"
	pickerDetail        skillPickerMode = "detail"
	pickerConfirmDelete skillPickerMode = "confirm-delete"
)

// skillPicker is the in-chat overlay for /skills manage. It lists discoverable
// skills with search, detail, and source views, following the same modal pattern
// as rewindPicker and resumePicker.
type skillPicker struct {
	mode            skillPickerMode
	skills          []skill.Skill
	roots           []skillRootLine
	enabled         map[string]bool
	originalEnabled map[string]bool
	query           string
	sel             int
	sourceSel       int
	sourceSkillSel  int
	showDiagnostics bool
	searchActive    bool
	detailSkill     skill.Skill
	detailBack      skillPickerMode
	detailAction    int
	confirm         int
	deleteSkill     skill.Skill
}

// skillRootLine is a CLI view model for one skill discovery root.
type skillRootLine struct {
	dir        string
	scope      skill.Scope
	status     skill.PathStatus
	skills     int
	configured bool
	diagnostic bool
}

// openSkillPicker populates the picker from m.skills and opens it. A no-op
// (with a notice) when there are no skills. Skills are sorted by scope
// priority (project > custom > global > builtin) then name.
func (m *chatTUI) openSkillPicker() {
	st := m.skillStore()
	skills := st.List()
	if m.ctrl != nil {
		skills = m.ctrl.AllSkills()
	}
	if len(skills) == 0 {
		m.notice(i18n.M.ListSkillsNone)
		return
	}
	sorted := sortedSkills(skills)
	enabled := map[string]bool{}
	original := map[string]bool{}
	disabled := m.disabledSkillNames()
	for _, sk := range sorted {
		on := !disabled[sk.Name]
		enabled[sk.Name] = on
		original[sk.Name] = on
	}
	m.skillPick = &skillPicker{
		mode:            pickerSkills,
		skills:          sorted,
		roots:           skillRootLines(st, sorted),
		enabled:         enabled,
		originalEnabled: original,
	}
}

func (m chatTUI) handleSkillPickerKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	p := m.skillPick
	if p == nil {
		return m, nil
	}

	// Search mode: typed chars build the query; Esc exits search.
	if p.searchActive {
		switch msg.String() {
		case "esc":
			p.searchActive = false
			return m, nil
		case "enter":
			return m.saveSkillPick()
		case "backspace":
			if len(p.query) > 0 {
				p.query = p.query[:len(p.query)-1]
				p.sel = clampSel(p.sel, p.filteredSkills())
			}
			return m, nil
		case "up", "k":
			if p.sel > 0 {
				p.sel--
			}
			return m, nil
		case "down", "j":
			filtered := p.filteredSkills()
			if p.sel < len(filtered)-1 {
				p.sel++
			}
			return m, nil
		default:
			// Append typed characters to the query.
			if t := msg.Text; t != "" {
				p.query += t
			} else if s := msg.String(); len(s) == 1 && s[0] >= 32 && s[0] < 127 {
				p.query += s
			}
			p.sel = clampSel(p.sel, p.filteredSkills())
			return m, nil
		}
	}

	// Non-search mode: route by current picker mode.
	switch p.mode {
	case pickerSkills:
		return m.handleSkillPickerSkillsKey(msg)
	case pickerSources:
		return m.handleSkillPickerSourcesKey(msg)
	case pickerSourceSkills:
		return m.handleSkillPickerSourceSkillsKey(msg)
	case pickerDetail:
		return m.handleSkillPickerDetailKey(msg)
	case pickerConfirmDelete:
		return m.handleSkillPickerConfirmDeleteKey(msg)
	}
	return m, nil
}

func (m chatTUI) handleSkillPickerSkillsKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	p := m.skillPick
	switch msg.String() {
	case "esc":
		m.skillPick = nil
	case "up", "k":
		if p.sel > 0 {
			p.sel--
		}
	case "down", "j":
		if p.sel < len(p.skills)-1 {
			p.sel++
		}
	case "enter":
		return m.saveSkillPick()
	case " ", "space":
		p.toggleSelectedSkill()
	case "right", "l":
		if sk, ok := p.selectedSkill(); ok {
			p.openDetail(sk, pickerSkills)
		}
	case "/":
		p.searchActive = true
		p.query = ""
	case "s":
		p.mode = pickerSources
		p.searchActive = false
		p.sourceSel = 0
	case "r":
		m.rescanSkills()
	}
	return m, nil
}

func (m chatTUI) handleSkillPickerSourcesKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	p := m.skillPick
	visible := p.visibleRoots()
	switch msg.String() {
	case "esc":
		m.skillPick = nil
	case "up", "k":
		if p.sourceSel > 0 {
			p.sourceSel--
		}
	case "down", "j":
		if p.sourceSel < len(visible)-1 {
			p.sourceSel++
		}
	case "enter", "right", "l":
		if len(visible) > 0 {
			p.mode = pickerSourceSkills
			p.sourceSkillSel = 0
		}
	case "d":
		p.showDiagnostics = !p.showDiagnostics
		p.sourceSel = clampSel(p.sourceSel, visible)
	case "s":
		p.mode = pickerSkills
		p.sourceSel = 0
		p.showDiagnostics = false
	case "r":
		m.rescanSkills()
	}
	return m, nil
}

func (m chatTUI) handleSkillPickerSourceSkillsKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	p := m.skillPick
	skills := p.selectedRootSkills()
	switch msg.String() {
	case "esc", "left", "h":
		p.mode = pickerSources
	case "up", "k":
		if p.sourceSkillSel > 0 {
			p.sourceSkillSel--
		}
	case "down", "j":
		if p.sourceSkillSel < len(skills)-1 {
			p.sourceSkillSel++
		}
	case "enter", "right", "l":
		if p.sourceSkillSel >= 0 && p.sourceSkillSel < len(skills) {
			p.openDetail(skills[p.sourceSkillSel], pickerSourceSkills)
		}
	case " ", "space":
		if p.sourceSkillSel >= 0 && p.sourceSkillSel < len(skills) {
			p.toggleSkill(skills[p.sourceSkillSel].Name)
		}
	}
	p.sourceSkillSel = clampSel(p.sourceSkillSel, skills)
	return m, nil
}

func (m chatTUI) handleSkillPickerDetailKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	p := m.skillPick
	actions := skillActionsFor(p.detailSkill)
	p.detailAction = clampInt(p.detailAction, len(actions))
	switch msg.String() {
	case "esc", "left", "h":
		p.mode = p.detailBack
		p.detailAction = 0
	case "up", "k":
		if p.detailAction > 0 {
			p.detailAction--
		}
	case "down", "j":
		if p.detailAction < len(actions)-1 {
			p.detailAction++
		}
	case "enter":
		if len(actions) > 0 {
			return m.applySkillAction(p.detailSkill, actions[p.detailAction])
		}
	case " ", "space":
		p.toggleSkill(p.detailSkill.Name)
	}
	p.detailAction = clampInt(p.detailAction, len(actions))
	return m, nil
}

func (m chatTUI) handleSkillPickerConfirmDeleteKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	p := m.skillPick
	switch msg.String() {
	case "up", "k", "down", "j":
		if p.confirm == 0 {
			p.confirm = 1
		} else {
			p.confirm = 0
		}
	case "y":
		p.confirm = 0
		return m.deleteSkillPick(p.deleteSkill)
	case "n", "esc":
		p.mode = pickerDetail
	case "enter":
		if p.confirm == 0 {
			return m.deleteSkillPick(p.deleteSkill)
		}
		p.mode = pickerDetail
	}
	return m, nil
}

func (m chatTUI) saveSkillPick() (tea.Model, tea.Cmd) {
	p := m.skillPick
	if p == nil {
		return m, nil
	}
	changes := p.changedEnabled()
	m.skillPick = nil
	if len(changes) == 0 {
		m.notice(i18n.M.SkillPickerNoChanges)
		return m, nil
	}
	m.skillSaveEnabledChanges(changes)
	if m.pendingModelSwitch != nil {
		return m, m.pendingModelSwitch
	}
	return m, nil
}

func (m chatTUI) deleteSkillPick(sk skill.Skill) (tea.Model, tea.Cmd) {
	p := m.skillPick
	target, ok, err := skillDeleteTarget(sk)
	if err != nil {
		m.notice("skill delete: " + err.Error())
		if p != nil {
			p.mode = pickerDetail
		}
		return m, nil
	}
	if !ok {
		m.notice("skill delete: built-in skills cannot be removed")
		if p != nil {
			p.mode = pickerDetail
		}
		return m, nil
	}
	if err := os.RemoveAll(target); err != nil {
		m.notice("skill delete: " + err.Error())
		if p != nil {
			p.mode = pickerDetail
		}
		return m, nil
	}
	if p != nil {
		delete(p.enabled, sk.Name)
		delete(p.originalEnabled, sk.Name)
		p.mode = pickerSkills
		p.detailAction = 0
		p.confirm = 1
	}
	m.notice(fmt.Sprintf(i18n.M.SkillPickerDeletedFmt, sk.Name))
	m.refreshSkillPickerData()
	m.scheduleSkillSessionRefresh("skill delete", "deleted skill "+sk.Name+" — refreshing session")
	return m, nil
}

// rescanSkills rebuilds the skill store and refreshes the picker data.
func (m *chatTUI) rescanSkills() {
	m.refreshSkillPickerData()
	m.notice(i18n.M.SkillPickerRescanned)
}

func (m *chatTUI) refreshSkillPickerData() {
	st := m.skillStore()
	skills := st.List()
	m.skills = skills
	if m.skillPick != nil {
		sorted := sortedSkills(skills)
		m.skillPick.skills = sorted
		m.skillPick.roots = skillRootLines(st, sorted)
		m.skillPick.syncEnabledMaps(sorted, m.disabledSkillNames())
		if m.skillPick.searchActive && m.skillPick.query != "" {
			m.skillPick.sel = clampSel(m.skillPick.sel, m.skillPick.filteredSkills())
		} else {
			m.skillPick.sel = clampSel(m.skillPick.sel, sorted)
		}
		m.skillPick.sourceSel = clampSel(m.skillPick.sourceSel, m.skillPick.visibleRoots())
		m.skillPick.sourceSkillSel = clampSel(m.skillPick.sourceSkillSel, m.skillPick.selectedRootSkills())
	}
}

// filteredSkills returns skills matching the current query (case-insensitive
// substring match on name and description).
func (p *skillPicker) filteredSkills() []skill.Skill {
	if p.query == "" {
		return p.skills
	}
	q := strings.ToLower(p.query)
	var out []skill.Skill
	for _, s := range p.skills {
		if strings.Contains(strings.ToLower(s.Name), q) ||
			strings.Contains(strings.ToLower(s.Description), q) {
			out = append(out, s)
		}
	}
	return out
}

// visibleRoots returns roots to display based on showDiagnostics.
func (p *skillPicker) visibleRoots() []skillRootLine {
	if p.showDiagnostics {
		return p.roots
	}
	var out []skillRootLine
	for _, r := range p.roots {
		if !r.diagnostic || r.configured || r.skills > 0 {
			out = append(out, r)
		}
	}
	return out
}

func (p *skillPicker) selectedSkill() (skill.Skill, bool) {
	if p == nil {
		return skill.Skill{}, false
	}
	skills := p.skills
	if p.searchActive && p.query != "" {
		skills = p.filteredSkills()
	}
	if len(skills) == 0 {
		return skill.Skill{}, false
	}
	p.sel = clampSel(p.sel, skills)
	return skills[p.sel], true
}

func (p *skillPicker) toggleSelectedSkill() {
	if sk, ok := p.selectedSkill(); ok {
		p.toggleSkill(sk.Name)
	}
}

func (p *skillPicker) openDetail(sk skill.Skill, back skillPickerMode) {
	p.detailSkill = sk
	p.detailBack = back
	p.detailAction = 0
	p.mode = pickerDetail
}

func (p *skillPicker) skillEnabled(name string) bool {
	if p == nil || p.enabled == nil {
		return true
	}
	if enabled, ok := p.enabled[name]; ok {
		return enabled
	}
	return true
}

func (p *skillPicker) toggleSkill(name string) {
	if p.enabled == nil {
		p.enabled = map[string]bool{}
	}
	if p.originalEnabled == nil {
		p.originalEnabled = map[string]bool{}
	}
	if _, ok := p.originalEnabled[name]; !ok {
		p.originalEnabled[name] = p.skillEnabled(name)
	}
	p.enabled[name] = !p.skillEnabled(name)
}

func (p *skillPicker) changedEnabled() map[string]bool {
	changes := map[string]bool{}
	if p == nil {
		return changes
	}
	for name, enabled := range p.enabled {
		original, ok := p.originalEnabled[name]
		if !ok {
			original = true
		}
		if enabled != original {
			changes[name] = enabled
		}
	}
	return changes
}

func (p *skillPicker) syncEnabledMaps(skills []skill.Skill, disabled map[string]bool) {
	nextEnabled := map[string]bool{}
	nextOriginal := map[string]bool{}
	for _, sk := range skills {
		enabled := !disabled[sk.Name]
		if p.enabled != nil {
			if existing, ok := p.enabled[sk.Name]; ok {
				enabled = existing
			}
		}
		original := !disabled[sk.Name]
		if p.originalEnabled != nil {
			if existing, ok := p.originalEnabled[sk.Name]; ok {
				original = existing
			}
		}
		nextEnabled[sk.Name] = enabled
		nextOriginal[sk.Name] = original
	}
	p.enabled = nextEnabled
	p.originalEnabled = nextOriginal
}

func (p *skillPicker) selectedRoot() (skillRootLine, bool) {
	if p == nil {
		return skillRootLine{}, false
	}
	roots := p.visibleRoots()
	if len(roots) == 0 {
		return skillRootLine{}, false
	}
	p.sourceSel = clampSel(p.sourceSel, roots)
	return roots[p.sourceSel], true
}

func (p *skillPicker) selectedRootSkills() []skill.Skill {
	root, ok := p.selectedRoot()
	if !ok {
		return nil
	}
	var out []skill.Skill
	for _, sk := range p.skills {
		if skillInRoot(sk, root) {
			out = append(out, sk)
		}
	}
	return out
}

func skillInRoot(sk skill.Skill, root skillRootLine) bool {
	if root.scope == skill.ScopeBuiltin {
		return sk.Scope == skill.ScopeBuiltin
	}
	if sk.Scope != root.scope || sk.Path == "" || sk.Scope == skill.ScopeBuiltin {
		return false
	}
	cleanPath := filepath.Clean(sk.Path)
	cleanRoot := filepath.Clean(root.dir)
	prefix := cleanRoot + string(filepath.Separator)
	return cleanPath == cleanRoot || strings.HasPrefix(cleanPath, prefix)
}

// skillRootLines builds the view-model list of discovery roots with per-root
// skill counts computed by path-prefix matching. Builtins get a virtual root.
func skillRootLines(st *skill.Store, skills []skill.Skill) []skillRootLine {
	storeRoots := st.Roots()
	lines := make([]skillRootLine, len(storeRoots))
	for i, r := range storeRoots {
		lines[i] = skillRootLine{
			dir:    r.Dir,
			scope:  r.Scope,
			status: r.Status,
		}
	}
	// Count skills per root by directory-boundary path match. Using
	// filepath.Clean + a trailing separator avoids /skills matching
	// /skills-extra.
	for _, s := range skills {
		if s.Scope == skill.ScopeBuiltin {
			continue
		}
		cleanPath := filepath.Clean(s.Path)
		for i := range lines {
			if lines[i].scope != s.Scope {
				continue
			}
			prefix := filepath.Clean(lines[i].dir) + string(filepath.Separator)
			if strings.HasPrefix(cleanPath, prefix) {
				lines[i].skills++
				break
			}
		}
	}
	// Mark custom paths as configured; convention dirs as diagnostic.
	for i := range lines {
		if lines[i].scope == skill.ScopeCustom {
			lines[i].configured = true
		} else {
			lines[i].diagnostic = true
		}
	}
	// Count builtins and append a virtual root.
	builtinCount := 0
	for _, s := range skills {
		if s.Scope == skill.ScopeBuiltin {
			builtinCount++
		}
	}
	if builtinCount > 0 {
		lines = append(lines, skillRootLine{
			dir:        i18n.M.SkillPickerBuiltinSource,
			scope:      skill.ScopeBuiltin,
			status:     skill.StatusOK,
			skills:     builtinCount,
			diagnostic: true,
		})
	}
	return lines
}

// -- rendering --

const (
	skillDialogMinRows = 8
	skillDialogMaxRows = 18
)

func (m chatTUI) renderSkillPicker() string {
	p := m.skillPick
	if p == nil {
		return ""
	}
	w := max(viewWidth(m.width), 40)
	switch p.mode {
	case pickerSkills:
		return managerContentPanelStyle(w).Render(m.renderSkillPickerSkills())
	case pickerSources:
		return managerContentPanelStyle(w).Render(m.renderSkillPickerSources())
	case pickerSourceSkills:
		return managerContentPanelStyle(w).Render(m.renderSkillPickerSourceSkills())
	case pickerDetail:
		return managerContentPanelStyle(w).Render(m.renderSkillPickerDetail())
	case pickerConfirmDelete:
		return managerContentPanelStyle(w).Render(m.renderSkillPickerConfirmDelete())
	}
	return ""
}

func (m chatTUI) skillPickerFooterHint() string {
	if m.skillPick == nil {
		return ""
	}
	switch m.skillPick.mode {
	case pickerSkills:
		return i18n.M.SkillPickerHint
	case pickerSources:
		return i18n.M.SkillPickerSourceHint
	case pickerSourceSkills:
		return i18n.M.SkillPickerSourceSkillsHint
	case pickerDetail:
		return i18n.M.SkillPickerDetailHint
	case pickerConfirmDelete:
		return i18n.M.SkillPickerDeleteHint
	default:
		return ""
	}
}

func (m chatTUI) renderSkillPickerSkills() string {
	p := m.skillPick
	w := max(viewWidth(m.width), 40)
	var b strings.Builder

	fmt.Fprintf(&b, "%s\n", viewHeader("Manage skills"))
	if summary := skillPickerSummary(p); summary != "" {
		fmt.Fprintf(&b, "%s\n", viewMeta(summary))
	}
	b.WriteByte('\n')
	b.WriteString(renderSkillSearchBox(p.query, p.searchActive, w))
	b.WriteByte('\n')

	skills := p.skills
	if p.searchActive && p.query != "" {
		skills = p.filteredSkills()
	}

	if len(skills) == 0 {
		b.WriteString(viewMeta(i18n.M.SkillPickerSearchEmpty))
		b.WriteByte('\n')
	} else {
		start, end := skillListWindow(p.sel, len(skills), m.skillPickerVisibleRows())
		if start > 0 {
			b.WriteString(viewMeta(fmt.Sprintf(i18n.M.SkillPickerMoreAboveFmt, start)))
			b.WriteByte('\n')
		}
		lastGroup := ""
		for i := start; i < end; i++ {
			group := skillGroupLabel(skills[i].Scope)
			if group != lastGroup {
				if lastGroup != "" {
					b.WriteByte('\n')
				}
				fmt.Fprintf(&b, "  %s\n", bold(group))
				lastGroup = group
			}
			b.WriteString(renderSkillRow(i+1, i == p.sel, skills[i], p.skillEnabled(skills[i].Name), w))
			b.WriteByte('\n')
		}
		if end < len(skills) {
			b.WriteString(viewMeta(fmt.Sprintf(i18n.M.SkillPickerMoreBelowFmt, len(skills)-end)))
			b.WriteByte('\n')
		}
	}

	return strings.TrimRight(b.String(), "\n")
}

func (m chatTUI) skillPickerVisibleRows() int {
	if m.height <= 0 {
		return skillDialogMaxRows
	}
	return min(skillDialogMaxRows, max(skillDialogMinRows, m.height-14))
}

func skillListWindow(sel, total, limit int) (int, int) {
	if total <= 0 {
		return 0, 0
	}
	if limit <= 0 || limit >= total {
		return 0, total
	}
	if sel < 0 {
		sel = 0
	}
	if sel >= total {
		sel = total - 1
	}
	start := sel - limit/2
	if start < 0 {
		start = 0
	}
	if start+limit > total {
		start = total - limit
	}
	return start, start + limit
}

func renderSkillSearchBox(query string, active bool, w int) string {
	boxWidth := max(8, w-4)
	innerWidth := max(1, boxWidth-4)
	text := "/ " + i18n.M.SkillPickerSearchPlaceholder
	if active || query != "" {
		text = "/ " + query
	}
	text = padRight(viewCompactText(text, innerWidth), innerWidth)
	var b strings.Builder
	b.WriteString(dim("  ╭" + strings.Repeat("─", boxWidth-2) + "╮"))
	b.WriteByte('\n')
	b.WriteString(dim("  │ " + text + " │"))
	b.WriteByte('\n')
	b.WriteString(dim("  ╰" + strings.Repeat("─", boxWidth-2) + "╯"))
	b.WriteByte('\n')
	return b.String()
}

func (m chatTUI) renderSkillPickerSources() string {
	p := m.skillPick
	var b strings.Builder

	b.WriteString(accent(i18n.M.SkillPickerSourceTitle))
	summary := skillSourceSummary(p.roots)
	if summary != "" {
		b.WriteString("  " + dim(summary))
	}
	b.WriteByte('\n')

	roots := p.visibleRoots()
	for i, r := range roots {
		label := sourceRowLabel(r, m.width)
		b.WriteString(rowLine(i == p.sourceSel, i+1, "", label, false))
		b.WriteByte('\n')
	}

	// Diagnostics toggle hint.
	if p.showDiagnostics {
		b.WriteString(dim("  " + i18n.M.SkillPickerDiagShown))
	} else {
		b.WriteString(dim("  " + i18n.M.SkillPickerDiagHidden))
	}
	return b.String()
}

func (m chatTUI) renderSkillPickerSourceSkills() string {
	p := m.skillPick
	var b strings.Builder
	root, ok := p.selectedRoot()
	if !ok {
		p.mode = pickerSources
		return m.renderSkillPickerSources()
	}
	skills := p.selectedRootSkills()
	b.WriteString(accent(i18n.M.SkillPickerSourceTitle))
	b.WriteString("  " + dim(viewCompactPath(root.dir, max(8, m.width-18))))
	b.WriteByte('\n')
	b.WriteByte('\n')
	if len(skills) == 0 {
		b.WriteString(dim("  " + i18n.M.SkillPickerSourceSkillsEmpty))
		b.WriteByte('\n')
		return b.String()
	}
	start, end := skillListWindow(p.sourceSkillSel, len(skills), m.skillPickerVisibleRows())
	if start > 0 {
		b.WriteString(dim("  " + fmt.Sprintf(i18n.M.SkillPickerMoreAboveFmt, start)))
		b.WriteByte('\n')
	}
	for i := start; i < end; i++ {
		b.WriteString(renderSkillRow(i+1, i == p.sourceSkillSel, skills[i], p.skillEnabled(skills[i].Name), m.width))
		b.WriteByte('\n')
	}
	if end < len(skills) {
		b.WriteString(dim("  " + fmt.Sprintf(i18n.M.SkillPickerMoreBelowFmt, len(skills)-end)))
		b.WriteByte('\n')
	}
	return b.String()
}

func (m chatTUI) renderSkillPickerDetail() string {
	p := m.skillPick
	var b strings.Builder
	b.WriteString(renderSkillDetailHeader(p.detailSkill, m.width))
	b.WriteByte('\n')
	b.WriteByte('\n')
	enabled := i18n.M.SkillPickerDisabledLabel
	if p.skillEnabled(p.detailSkill.Name) {
		enabled = i18n.M.SkillPickerAvailableLabel
	}
	b.WriteString(dim("  " + i18n.M.SkillPickerStatusLabel + ": " + enabled))
	b.WriteByte('\n')
	actions := skillActionsFor(p.detailSkill)
	for i, action := range actions {
		b.WriteString(rowLine(i == p.detailAction, i+1, "", action.label, false))
		b.WriteByte('\n')
	}
	if body := renderSkillBodyPreview(p.detailSkill, m.width, 6); body != "" {
		b.WriteByte('\n')
		b.WriteString(body)
	}
	return b.String()
}

func (m chatTUI) renderSkillPickerConfirmDelete() string {
	p := m.skillPick
	var b strings.Builder
	b.WriteString(accent(fmt.Sprintf(i18n.M.SkillPickerDeleteTitleFmt, p.deleteSkill.Name)))
	b.WriteByte('\n')
	path := skillDeleteTargetLabel(p.deleteSkill)
	if path != "" {
		b.WriteString(dim("  " + viewCompactPath(path, max(8, m.width-4))))
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	b.WriteString(rowLine(p.confirm == 0, 1, "", i18n.M.SkillPickerDeleteConfirm, false))
	b.WriteByte('\n')
	b.WriteString(rowLine(p.confirm == 1, 2, "", i18n.M.SkillPickerDeleteCancel, false))
	return b.String()
}

// renderSkillRow renders one skill as a single-line picker row:
//
//  12. ✓ 可用  brand-guidelines       · 全局 · ~80 tok
//
// The selected row gets full reverse-video highlighting.
func renderSkillRow(num int, selected bool, s skill.Skill, enabled bool, w int) string {
	prefix := "    "
	if selected {
		prefix = accent("  › ")
	}
	nameWidth := min(30, max(14, w/3))
	name := compactMiddle(s.Name, nameWidth)
	if selected {
		name = bold(name)
	}
	name = padRight(name, nameWidth)
	status := "✓ " + i18n.M.SkillPickerAvailableLabel
	if enabled {
		status = viewStatus(status)
	} else {
		status = viewMeta("○ " + i18n.M.SkillPickerDisabledLabel)
	}
	meta := skillRowMeta(s)
	number := fmt.Sprintf("%2d. ", num)
	line := fmt.Sprintf("%s%s%s · %s · %s", prefix, number, name, status, viewMeta(meta))
	if visibleWidth(line) > w {
		line = viewCompactText(line, w)
	}

	if selected {
		return reverse(padRight(line, w))
	}
	return line
}

func skillGroupLabel(sc skill.Scope) string {
	return titleText(scopeLabel(sc)) + " skills"
}

func skillRowMeta(s skill.Skill) string {
	parts := []string{scopeLabel(s.Scope)}
	if s.RunAs == skill.RunSubagent {
		parts = append(parts, i18n.M.SkillPickerSubagent)
	}
	parts = append(parts, fmt.Sprintf(i18n.M.SkillPickerTokenFmt, approxSkillTokens(s)))
	return strings.Join(parts, " · ")
}

func approxSkillTokens(s skill.Skill) int {
	text := strings.TrimSpace(s.Body)
	if text == "" {
		text = strings.TrimSpace(s.Description)
	}
	if text == "" {
		return 0
	}
	estimate := max(len([]rune(text))/4, len(strings.Fields(text)))
	if estimate <= 10 {
		return 10
	}
	return ((estimate + 9) / 10) * 10
}

// sourceRowLabel formats one root as "path  scope  status  N skills".
func sourceRowLabel(r skillRootLine, w int) string {
	path := viewCompactPath(r.dir, max(8, w-40))
	scope := dim(scopeLabel(r.scope))
	status := statusLabel(r.status)
	if r.status == skill.StatusOK {
		status = accent(status)
	} else {
		status = dim(status)
	}
	skills := dim(fmt.Sprintf("%d %s", r.skills, i18n.M.SkillPickerSkillsUnit))
	return fmt.Sprintf("%s  %s  %s  %s", path, scope, status, skills)
}

// skillPickerSummary builds the summary line for the skills header. When
// searching it shows "N matching · total M"; otherwise "N available · X
// project · Y builtin" with only non-zero scopes.
func skillPickerSummary(p *skillPicker) string {
	if len(p.skills) == 0 {
		return ""
	}
	if p.searchActive && p.query != "" {
		filtered := p.filteredSkills()
		return fmt.Sprintf(i18n.M.SkillPickerMatchingFmt, len(filtered), len(p.skills))
	}
	counts := map[skill.Scope]int{}
	for _, s := range p.skills {
		counts[s.Scope]++
	}
	var parts []string
	parts = append(parts, fmt.Sprintf(i18n.M.SkillPickerAvailableFmt, len(p.skills)))
	for _, sc := range []skill.Scope{skill.ScopeProject, skill.ScopeCustom, skill.ScopeGlobal, skill.ScopeBuiltin} {
		if n, ok := counts[sc]; ok && n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, scopeLabel(sc)))
		}
	}
	return strings.Join(parts, " · ")
}

// skillSourceSummary builds the "N active" line for the source view.
func skillSourceSummary(roots []skillRootLine) string {
	active := 0
	for _, r := range roots {
		if r.skills > 0 {
			active++
		}
	}
	if active == 0 {
		return ""
	}
	return fmt.Sprintf(i18n.M.SkillPickerSourceActiveFmt, active)
}

// renderSkillDetail renders the detail pane for one skill.
func renderSkillDetail(s skill.Skill, w int) string {
	var b strings.Builder
	b.WriteString(renderSkillDetailHeader(s, w))
	if body := renderSkillBodyPreview(s, w, 12); body != "" {
		b.WriteByte('\n')
		b.WriteString(body)
	}
	return b.String()
}

func renderSkillDetailHeader(s skill.Skill, w int) string {
	var b strings.Builder
	b.WriteString(accent("/" + s.Name))
	b.WriteByte('\n')

	// Scope + RunAs.
	meta := fmt.Sprintf(i18n.M.SkillPickerDetailMetaFmt, scopeLabel(s.Scope), string(s.RunAs))
	b.WriteString(dim("  " + meta))
	b.WriteByte('\n')

	// Path.
	if s.Path != "" && s.Scope != skill.ScopeBuiltin {
		b.WriteString(dim("  " + viewCompactPath(s.Path, max(8, w-4))))
		b.WriteByte('\n')
	}

	// Description.
	if strings.TrimSpace(s.Description) != "" {
		b.WriteByte('\n')
		b.WriteString(viewCompactText(s.Description, max(8, w-4)))
		b.WriteByte('\n')
	}

	return b.String()
}

func renderSkillBodyPreview(s skill.Skill, w, maxLines int) string {
	body, extra := viewBodyPreview(s.Body, maxLines)
	if body == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString(dim(viewProtectLines(body, w)))
	b.WriteByte('\n')
	if extra > 0 {
		b.WriteString(viewMore(extra, i18n.M.SkillPickerLinesUnit))
		b.WriteByte('\n')
	}
	return b.String()
}

type skillActionKind string

const (
	skillActionToggle skillActionKind = "toggle"
	skillActionDelete skillActionKind = "delete"
)

type skillActionItem struct {
	kind  skillActionKind
	label string
}

func skillActionsFor(s skill.Skill) []skillActionItem {
	actions := []skillActionItem{{
		kind:  skillActionToggle,
		label: i18n.M.SkillPickerActionToggle,
	}}
	if _, ok, _ := skillDeleteTarget(s); ok {
		actions = append(actions, skillActionItem{
			kind:  skillActionDelete,
			label: i18n.M.SkillPickerActionDelete,
		})
	}
	return actions
}

func (m chatTUI) applySkillAction(sk skill.Skill, action skillActionItem) (tea.Model, tea.Cmd) {
	p := m.skillPick
	if p == nil {
		return m, nil
	}
	switch action.kind {
	case skillActionToggle:
		p.toggleSkill(sk.Name)
	case skillActionDelete:
		p.deleteSkill = sk
		p.confirm = 1
		p.mode = pickerConfirmDelete
	}
	return m, nil
}

func skillDeleteTarget(s skill.Skill) (string, bool, error) {
	if s.Scope == skill.ScopeBuiltin {
		return "", false, nil
	}
	path := strings.TrimSpace(s.Path)
	if path == "" || path == "(builtin)" {
		return "", false, nil
	}
	clean := filepath.Clean(path)
	info, err := os.Stat(clean)
	if err != nil {
		return "", false, err
	}
	if filepath.Base(clean) == skill.SkillFile {
		dir := filepath.Dir(clean)
		if dir == "." || dir == string(filepath.Separator) {
			return "", false, fmt.Errorf("refusing to remove unsafe skill directory %q", dir)
		}
		return dir, true, nil
	}
	if info.Mode().IsRegular() {
		return clean, true, nil
	}
	return "", false, fmt.Errorf("skill path is not removable: %s", clean)
}

func skillDeleteTargetLabel(s skill.Skill) string {
	target, ok, _ := skillDeleteTarget(s)
	if !ok {
		return ""
	}
	return target
}

func scopeLabel(sc skill.Scope) string {
	switch sc {
	case skill.ScopeProject:
		return i18n.M.SkillPickerScopeProject
	case skill.ScopeCustom:
		return i18n.M.SkillPickerScopeCustom
	case skill.ScopeGlobal:
		return i18n.M.SkillPickerScopeGlobal
	case skill.ScopeBuiltin:
		return i18n.M.SkillPickerScopeBuiltin
	default:
		return string(sc)
	}
}

func statusLabel(st skill.PathStatus) string {
	switch st {
	case skill.StatusOK:
		return i18n.M.SkillPickerStatusOK
	case skill.StatusMissing:
		return i18n.M.SkillPickerStatusMissing
	case skill.StatusNotDirectory:
		return i18n.M.SkillPickerStatusNotDir
	case skill.StatusUnreadable:
		return i18n.M.SkillPickerStatusUnreadable
	default:
		return string(st)
	}
}

// sortedSkills returns a copy of skills sorted by scope priority (project >
// custom > global > builtin) then alphabetically by name.
func sortedSkills(skills []skill.Skill) []skill.Skill {
	sorted := make([]skill.Skill, len(skills))
	copy(sorted, skills)
	sort.SliceStable(sorted, func(i, j int) bool {
		pi := scopePriority[sorted[i].Scope]
		pj := scopePriority[sorted[j].Scope]
		if pi != pj {
			return pi < pj
		}
		return sorted[i].Name < sorted[j].Name
	})
	return sorted
}

func clampSel[T any](sel int, items []T) int {
	if len(items) == 0 {
		return 0
	}
	if sel < 0 {
		return 0
	}
	if sel >= len(items) {
		return len(items) - 1
	}
	return sel
}

func clampInt(sel, total int) int {
	if total <= 0 {
		return 0
	}
	if sel < 0 {
		return 0
	}
	if sel >= total {
		return total - 1
	}
	return sel
}
