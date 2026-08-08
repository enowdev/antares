package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/enowdev/antares/internal/providers"
)

// ---- theme picker ------------------------------------------------------------

func (m *Model) openThemePicker() {
	origin := m.themeName
	names := themeNames()
	items := make([]pickerItem, len(names))
	cursor := 0
	for i, n := range names {
		items[i] = pickerItem{id: n, label: n, right: swatch(themeByName(n))}
		if n == m.themeName {
			cursor = i
		}
	}
	m.openPicker(picker{
		title:   "Select a theme",
		hint:    "previews live",
		footer:  "type to filter · ↑↓ move · Enter apply · Esc cancel",
		items:   items,
		cursor:  cursor,
		preview: func(m *Model, it pickerItem) { m.applyTheme(it.id) },
		commit: func(m *Model, it pickerItem) {
			m.applyTheme(it.id)
			m.persistTheme(it.id)
			m.setStatus("theme → " + it.id)
		},
		cancel: func(m *Model) { m.applyTheme(origin) },
	})
}

// ---- model picker ------------------------------------------------------------

type modelRef struct{ id, prov string }

// modelsFetchedMsg carries models pulled live from provider endpoints.
type modelsFetchedMsg struct{ refs []modelRef }

// collectConfigModels gathers the statically-configured models of the *active*
// provider only, plus the current default. Other providers' models are not
// shown — switch provider with /provider first.
func (m *Model) collectConfigModels() []modelRef {
	var list []modelRef
	seen := map[string]bool{}
	add := func(id, prov string) {
		if id == "" || seen[prov+"\x00"+id] {
			return
		}
		seen[prov+"\x00"+id] = true
		list = append(list, modelRef{id, prov})
	}
	prov := m.cfg.Model.Provider
	if prov != "" {
		for _, mid := range m.cfg.Providers[prov].Models {
			add(mid, prov)
		}
	}
	add(m.cfg.Model.Default, prov)
	return list
}

// fetchableProviders is the active provider alone (when it can be reached), so
// the model list only ever reflects the provider currently in use.
// Providers with a non-empty curated models list are skipped — that list is
// already the complete whitelist (no live catalog merge).
func (m *Model) fetchableProviders() []string {
	prov := m.cfg.Model.Provider
	if prov == "" {
		return nil
	}
	p := m.cfg.Providers[prov]
	if len(p.Models) > 0 {
		return nil
	}
	if providers.Connected(m.cfg, prov) || p.BaseURL != "" {
		return []string{prov}
	}
	return nil
}

func (m *Model) modelItem(r modelRef) pickerItem {
	right := ""
	if r.prov != "" {
		right = lipgloss.NewStyle().Foreground(themeByName(m.themeName).Faint).Render(r.prov)
	}
	return pickerItem{id: r.id, label: r.id, right: right, meta: r.prov}
}

func (m *Model) openModelPicker() tea.Cmd {
	m.reloadConfig() // reflect models added via dashboard / CLI / file edits
	if m.cfg == nil {
		m.pushSystem("No config loaded.")
		return nil
	}
	list := m.collectConfigModels()
	fetch := m.fetchableProviders()
	if len(list) == 0 && len(fetch) == 0 {
		m.pushSystem("No models available. Use /provider to connect a provider first.")
		return nil
	}

	items := make([]pickerItem, len(list))
	cursor := 0
	for i, r := range list {
		items[i] = m.modelItem(r)
		if r.id == m.cfg.Model.Default {
			cursor = i
		}
	}
	prov := m.cfg.Model.Provider
	hint := fmt.Sprintf("%s · %d", prov, len(items))
	if len(fetch) > 0 {
		hint = prov + " · fetching…"
	}
	m.openPicker(picker{
		title:  "Select a model",
		hint:   hint,
		footer: "type to filter · ↑↓ move · Enter apply · Esc cancel",
		items:  items,
		cursor: cursor,
		commit: func(m *Model, it pickerItem) {
			prev := m.cfg.Model.Provider
			m.cfg.Model.Default = it.id
			if it.meta != "" {
				m.cfg.Model.Provider = it.meta
			}
			if m.cfg.Model.Provider != prev {
				m.cfg.ClearInlineModelCredentials()
			} else if p, ok := m.cfg.Providers[m.cfg.Model.Provider]; ok &&
				(strings.TrimSpace(p.BaseURL) != "" || strings.TrimSpace(p.APIKey) != "") {
				m.cfg.ClearInlineModelCredentials()
			}
			m.saveConfig()
			m.setStatus("model → " + it.id)
		},
	})
	return m.fetchModelsCmd(fetch)
}

// fetchModelsCmd queries the given providers' /models endpoints concurrently.
func (m *Model) fetchModelsCmd(provs []string) tea.Cmd {
	if len(provs) == 0 || m.cfg == nil {
		return nil
	}
	cfg := m.cfg
	return func() tea.Msg {
		var mu sync.Mutex
		var refs []modelRef
		var wg sync.WaitGroup
		for _, pid := range provs {
			wg.Add(1)
			go func(pid string) {
				defer wg.Done()
				ids, err := providers.FetchModels(context.Background(), cfg, pid)
				if err != nil {
					return
				}
				mu.Lock()
				for _, id := range ids {
					refs = append(refs, modelRef{id, pid})
				}
				mu.Unlock()
			}(pid)
		}
		wg.Wait()
		return modelsFetchedMsg{refs}
	}
}

// mergeFetchedModels folds live-fetched models into the open model picker.
func (m *Model) mergeFetchedModels(refs []modelRef) {
	if !m.picker.active || m.picker.title != "Select a model" {
		return
	}
	seen := map[string]bool{}
	for _, it := range m.picker.items {
		seen[it.meta+"\x00"+it.id] = true
	}
	for _, r := range refs {
		k := r.prov + "\x00" + r.id
		if r.id == "" || seen[k] {
			continue
		}
		seen[k] = true
		m.picker.items = append(m.picker.items, m.modelItem(r))
	}
	m.picker.hint = fmt.Sprintf("%s · %d", m.cfg.Model.Provider, len(m.picker.items))
}

// ---- provider picker + connect flow -----------------------------------------

func (m *Model) openProviderPicker() {
	m.reloadConfig() // reflect providers added via dashboard / CLI / file edits
	if m.cfg == nil {
		m.pushSystem("No config loaded.")
		return
	}
	t := themeByName(m.themeName)

	// Catalog first, then any configured providers not in the catalog.
	ids := make([]string, 0)
	for _, p := range providers.Catalog() {
		ids = append(ids, p.ID)
	}
	extra := make([]string, 0)
	for id := range m.cfg.Providers {
		if _, ok := providers.For(id); !ok {
			extra = append(extra, id)
		}
	}
	sort.Strings(extra)
	ids = append(ids, extra...)

	items := make([]pickerItem, 0, len(ids))
	cursor := 0
	for _, id := range ids {
		label := id
		if info, ok := providers.For(id); ok {
			label = info.Label
		} else if p, ok := m.cfg.Providers[id]; ok && p.Label != "" {
			label = p.Label
		}
		var status string
		if providers.Connected(m.cfg, id) {
			status = lipgloss.NewStyle().Foreground(t.Green).Render("● connected")
		} else {
			status = lipgloss.NewStyle().Foreground(t.Faint).Render("○ connect")
		}
		if id == m.cfg.Model.Provider {
			status += lipgloss.NewStyle().Foreground(t.Accent).Render("  active")
			cursor = len(items)
		}
		items = append(items, pickerItem{id: id, label: label, right: status})
	}
	m.openPicker(picker{
		title:  "Providers",
		hint:   "connect or switch",
		footer: "type to filter · ↑↓ move · Enter select · Esc cancel",
		items:  items,
		cursor: cursor,
		commit: func(m *Model, it pickerItem) { m.selectProvider(it.id) },
	})
}

// selectProvider switches to a connected provider, or opens a key prompt to
// connect one that is not yet set up.
func (m *Model) selectProvider(id string) {
	if providers.Connected(m.cfg, id) {
		m.activateProvider(id, "")
		return
	}
	info, ok := providers.For(id)
	if !ok {
		m.activateProvider(id, "") // unknown/custom provider — just switch to it
		return
	}
	m.openInput(
		"Connect "+info.Label,
		"Paste your API key — stored in your local config only.",
		"sk-…", true,
		func(m *Model, key string) {
			if key == "" {
				m.setStatus("connect cancelled")
				return
			}
			m.activateProvider(id, key)
		},
		func(m *Model) { m.setStatus("connect cancelled") },
	)
}

// activateProvider connects/switches to a provider and persists the change.
func (m *Model) activateProvider(id, key string) {
	providers.Activate(m.cfg, id, key)
	m.saveConfig()
	if key != "" {
		m.setStatus("connected " + id)
		m.pushSystem("Connected to " + id + ". Model set to " + m.cfg.Model.Default + ". Use /model to change it.")
	} else {
		m.setStatus("provider → " + id)
	}
}
