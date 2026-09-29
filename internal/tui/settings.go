package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// settingsEntry is one row of the Ctrl+P settings menu. value shows the
// current setting on the right; run applies it — most open the picker the
// matching slash command opens, the toggles flip in place.
type settingsEntry struct {
	id, label string
	value     func(m *Model) string
	run       func(m *Model) tea.Cmd
}

// settingsEntries is a function, not a var: the toggles reopen the menu,
// which reads this list.
func settingsEntries() []settingsEntry {
	return []settingsEntry{
		{"model", "Model", func(m *Model) string { return m.modelLabel() },
			func(m *Model) tea.Cmd { return m.openModelPicker() }},
		{"provider", "Provider", func(m *Model) string {
			if m.cfg == nil {
				return ""
			}
			return m.cfg.Model.Provider
		}, func(m *Model) tea.Cmd { m.openProviderPicker(); return nil }},
		{"effort", "Reasoning effort", func(m *Model) string {
			if m.effort == "" {
				return "auto"
			}
			return m.effort
		}, func(m *Model) tea.Cmd { _, cmd := m.cmdEffort(""); return cmd }},
		{"theme", "Theme", func(m *Model) string { return m.themeName },
			func(m *Model) tea.Cmd { m.openThemePicker(); return nil }},
		{"reasoning", "Show reasoning", func(m *Model) string { return onOff(m.showReasoning) },
			func(m *Model) tea.Cmd {
				m.showReasoning = !m.showReasoning
				m.setStatus("reasoning " + onOff(m.showReasoning))
				m.refreshTranscript()
				m.openSettings("reasoning")
				return nil
			}},
		{"side", "Side column", func(m *Model) string { return onOff(!m.chrome.sideHidden) },
			func(m *Model) tea.Cmd {
				m.chrome.sideHidden = !m.chrome.sideHidden
				m.refreshTranscript()
				m.openSettings("side")
				return nil
			}},
		{"project", "Project folder", func(m *Model) string {
			if m.projectDir == "" {
				return "none"
			}
			return m.projectDir
		}, func(m *Model) tea.Cmd { m.prefill("/project "); return nil }},
	}
}

// openSettings shows the settings menu, with the cursor on the row whose id is
// at (the toggles reopen the menu on themselves so several can be flipped).
func (m *Model) openSettings(at string) {
	entries := settingsEntries()
	items := make([]pickerItem, len(entries))
	cursor := 0
	for i, e := range entries {
		items[i] = pickerItem{id: e.id, label: e.label, right: m.st.headerDim.Render(e.value(m))}
		if e.id == at {
			cursor = i
		}
	}
	m.openPicker(picker{
		title:  "Settings",
		hint:   "Enter to change",
		footer: "type to filter · ↑↓ move · Enter change · Esc close",
		items:  items,
		cursor: cursor,
		commit: func(m *Model, it pickerItem) {
			for _, e := range entries {
				if e.id == it.id {
					m.after = e.run(m)
					return
				}
			}
		},
	})
}

// prefill puts text in the input for the user to finish, e.g. a command that
// needs an argument.
func (m *Model) prefill(text string) {
	m.ta.SetValue(text)
	m.ta.CursorEnd()
	m.syncCursor()
}
