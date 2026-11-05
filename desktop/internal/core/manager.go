// Package core is the desktop shell's behaviour without the window toolkit:
// saved connections, pairing, opening a connection through a handoff code,
// the local daemon, and the health poll that drives the reconnect page. The
// Wails layer (package main) adapts it to a window, a tray and a menu.
package core

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/enowdev/antares/desktop/internal/api"
	"github.com/enowdev/antares/desktop/internal/conns"
	"github.com/enowdev/antares/desktop/internal/daemon"
	"github.com/enowdev/antares/desktop/internal/secrets"
)

// Navigator is the window, as the core sees it.
type Navigator interface {
	ShowConnections()    // the bundled connection screen
	ShowReconnect()      // the bundled "Reconnecting…" page
	Navigate(url string) // an absolute dashboard URL
	Changed()            // state changed: refresh tray and pages
}

// LocalCLI is the part of the antares CLI the core drives.
type LocalCLI interface {
	Pair(ctx context.Context, name, platform string) (daemon.PairResult, error)
	Revoke(ctx context.Context, id string) error
	Serve(ctx context.Context) error
	Stop(ctx context.Context) error
}

// Deps are the Manager's collaborators; tests swap any of them.
type Deps struct {
	Store       *conns.Store
	Secrets     secrets.Store
	Resolver    daemon.Resolver
	Probe       daemon.Probe
	CLI         func(bin string) LocalCLI            // default: daemon.CLI
	NewClient   func(base, token string) *api.Client // default: api.New
	Nav         Navigator
	MachineName string
	Platform    string // desktop-macos | desktop-windows | desktop-linux
	AppVersion  string
	Log         *slog.Logger

	PollEvery      time.Duration // default 10s
	ReconnectEvery time.Duration // default 3s
	StartTimeout   time.Duration // default daemon.StartTimeout
}

// Phase is what the main window shows.
type Phase string

const (
	PhaseConnections  Phase = "connections"
	PhaseOpening      Phase = "opening"
	PhaseOpen         Phase = "open"
	PhaseReconnecting Phase = "reconnecting"
)

// Connection statuses shown as the dot on the connection screen.
const (
	StatusUnknown      = "unknown"
	StatusOnline       = "online"
	StatusOffline      = "offline"
	StatusSignedOut    = "signed_out"
	StatusRunning      = "running"
	StatusStopped      = "stopped"
	StatusNotInstalled = "not_installed"
)

// ConnView is one row on the connection screen.
type ConnView struct {
	ID      string     `json:"id"`
	Name    string     `json:"name"`
	Mode    conns.Mode `json:"mode"`
	URL     string     `json:"url"`
	Status  string     `json:"status"`
	Current bool       `json:"current"`
	Last    bool       `json:"last"`
}

// LocalView is "This Mac": is antares installed, is the daemon running.
type LocalView struct {
	Installed bool   `json:"installed"`
	Binary    string `json:"binary,omitempty"`
	Source    string `json:"source,omitempty"` // bundled | path | local-bin
	Running   bool   `json:"running"`
	URL       string `json:"url,omitempty"`
	Version   string `json:"version,omitempty"`
	Saved     bool   `json:"saved"` // a local connection exists
}

// State is everything the bundled pages and the tray render.
type State struct {
	Connections []ConnView `json:"connections"`
	Local       LocalView  `json:"local"`
	Current     string     `json:"current"`
	CurrentName string     `json:"current_name"`
	Phase       Phase      `json:"phase"`
	Notice      string     `json:"notice"`
	Platform    string     `json:"platform"`
	Machine     string     `json:"machine"`
	Version     string     `json:"version"`
}

// Manager owns the shell's state. Safe for concurrent use.
type Manager struct {
	d Deps

	mu          sync.Mutex
	status      map[string]string
	local       LocalView
	current     string
	currentBase string
	path        string // last dashboard path seen in the window
	phase       Phase
	notice      string
	pollCancel  context.CancelFunc
	openSeq     int
	lastRecheck time.Time
	localProbed bool
}

// New builds a Manager; Deps.Store, Secrets and Nav are required.
func New(d Deps) *Manager {
	if d.CLI == nil {
		d.CLI = func(bin string) LocalCLI { return daemon.CLI{Bin: bin} }
	}
	if d.NewClient == nil {
		d.NewClient = api.New
	}
	if d.Log == nil {
		d.Log = slog.Default()
	}
	if d.PollEvery == 0 {
		d.PollEvery = 10 * time.Second
	}
	if d.ReconnectEvery == 0 {
		d.ReconnectEvery = 3 * time.Second
	}
	if d.StartTimeout == 0 {
		d.StartTimeout = daemon.StartTimeout
	}
	if d.MachineName == "" {
		d.MachineName = "Desktop"
	}
	return &Manager{d: d, status: map[string]string{}, phase: PhaseConnections}
}

// State snapshots the current state. The first call probes "This Mac" (a
// stat and the daemon state file) so the screen never starts out wrong.
func (m *Manager) State() State {
	m.mu.Lock()
	probed := m.localProbed
	m.mu.Unlock()
	if !probed {
		m.refreshLocal(context.Background())
	}
	list := m.d.Store.List()
	last := m.d.Store.Last()
	m.mu.Lock()
	defer m.mu.Unlock()
	st := State{
		Connections: make([]ConnView, 0, len(list)),
		Local:       m.local,
		Current:     m.current,
		Phase:       m.phase,
		Notice:      m.notice,
		Platform:    m.d.Platform,
		Machine:     m.d.MachineName,
		Version:     m.d.AppVersion,
	}
	for _, c := range list {
		s := m.status[c.ID]
		if s == "" {
			s = StatusUnknown
		}
		if c.Mode == conns.ModeLocal {
			st.Local.Saved = true
		}
		st.Connections = append(st.Connections, ConnView{
			ID: c.ID, Name: c.Name, Mode: c.Mode, URL: c.URL, Status: s,
			Current: c.ID == m.current, Last: c.ID == last,
		})
		if c.ID == m.current {
			st.CurrentName = c.Name
		}
	}
	return st
}

// Refresh probes every connection (remote health, local daemon) and returns
// the new state.
func (m *Manager) Refresh(ctx context.Context) State {
	m.refreshLocal(ctx)
	var wg sync.WaitGroup
	for _, c := range m.d.Store.List() {
		if c.Mode != conns.ModeRemote {
			continue
		}
		wg.Add(1)
		go func(c conns.Connection) {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, 4*time.Second)
			defer cancel()
			err := m.d.NewClient(c.URL, "").Health(cctx)
			m.mu.Lock()
			if m.status[c.ID] != StatusSignedOut {
				if err == nil {
					m.status[c.ID] = StatusOnline
				} else {
					m.status[c.ID] = StatusOffline
				}
			}
			m.mu.Unlock()
		}(c)
	}
	wg.Wait()
	m.d.Nav.Changed()
	return m.State()
}

func (m *Manager) refreshLocal(ctx context.Context) LocalView {
	lv := LocalView{}
	if c, err := m.d.Resolver.Resolve(); err == nil {
		lv.Installed, lv.Binary, lv.Source = true, c.Path, c.Source
	}
	st, _ := m.d.Probe.Status(ctx)
	lv.Running, lv.URL, lv.Version = st.Running, st.URL, st.Version
	m.mu.Lock()
	m.local = lv
	m.localProbed = true
	if c, ok := m.d.Store.Local(); ok && m.status[c.ID] != StatusSignedOut {
		switch {
		case !lv.Installed && !lv.Running:
			m.status[c.ID] = StatusNotInstalled
		case lv.Running:
			m.status[c.ID] = StatusRunning
		default:
			m.status[c.ID] = StatusStopped
		}
	}
	m.mu.Unlock()
	return lv
}

// AddRemote checks the server, pairs this machine with password and saves
// the connection (token to the keychain).
func (m *Manager) AddRemote(ctx context.Context, name, rawURL, password string) (ConnView, error) {
	base, err := conns.ValidateURL(rawURL)
	if err != nil {
		return ConnView{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = hostOf(base)
	}
	client := m.d.NewClient(base, "")
	if _, err := client.Version(ctx, m.d.AppVersion); err != nil {
		return ConnView{}, err
	}
	p, err := client.Pair(ctx, m.d.MachineName, m.d.Platform, password)
	if err != nil {
		return ConnView{}, err
	}
	c := conns.Connection{ID: conns.NewID(), Name: name, Mode: conns.ModeRemote, URL: base, DeviceID: p.Device.ID}
	return m.save(ctx, c, p.Token, func() {
		_ = m.d.NewClient(base, p.Token).RevokeDevice(context.Background(), p.Device.ID)
	})
}

// AddLocal pairs through the antares CLI and saves the "This Mac" connection.
func (m *Manager) AddLocal(ctx context.Context) (ConnView, error) {
	if _, ok := m.d.Store.Local(); ok {
		return ConnView{}, conns.ErrLocalExists
	}
	bin, err := m.d.Resolver.Resolve()
	if err != nil {
		return ConnView{}, err
	}
	cli := m.d.CLI(bin.Path)
	p, err := cli.Pair(ctx, m.d.MachineName, m.d.Platform)
	if err != nil {
		return ConnView{}, err
	}
	c := conns.Connection{ID: conns.NewID(), Name: "This Mac", Mode: conns.ModeLocal, DeviceID: p.Device.ID}
	if m.d.Platform != "desktop-macos" {
		c.Name = "This computer"
	}
	return m.save(ctx, c, p.Token, func() { _ = cli.Revoke(context.Background(), p.Device.ID) })
}

func (m *Manager) save(ctx context.Context, c conns.Connection, token string, undo func()) (ConnView, error) {
	if err := m.d.Secrets.Set(c.ID, token); err != nil {
		undo()
		return ConnView{}, fmt.Errorf("save the token in the keychain: %w", err)
	}
	saved, err := m.d.Store.Add(c)
	if err != nil {
		_ = m.d.Secrets.Delete(c.ID)
		undo()
		return ConnView{}, err
	}
	m.mu.Lock()
	m.notice = ""
	m.mu.Unlock()
	m.Refresh(ctx)
	for _, v := range m.State().Connections {
		if v.ID == saved.ID {
			return v, nil
		}
	}
	return ConnView{ID: saved.ID, Name: saved.Name, Mode: saved.Mode, URL: saved.URL}, nil
}

// Repair pairs a signed-out connection again (password for remote; the CLI
// for local) and keeps its id, name and place in the list.
func (m *Manager) Repair(ctx context.Context, id, password string) error {
	c, err := m.d.Store.Get(id)
	if err != nil {
		return err
	}
	var token, device string
	switch c.Mode {
	case conns.ModeRemote:
		client := m.d.NewClient(c.URL, "")
		if _, err := client.Version(ctx, m.d.AppVersion); err != nil {
			return err
		}
		p, err := client.Pair(ctx, m.d.MachineName, m.d.Platform, password)
		if err != nil {
			return err
		}
		token, device = p.Token, p.Device.ID
	default:
		bin, err := m.d.Resolver.Resolve()
		if err != nil {
			return err
		}
		p, err := m.d.CLI(bin.Path).Pair(ctx, m.d.MachineName, m.d.Platform)
		if err != nil {
			return err
		}
		token, device = p.Token, p.Device.ID
	}
	if err := m.d.Secrets.Set(c.ID, token); err != nil {
		return fmt.Errorf("save the token in the keychain: %w", err)
	}
	c.DeviceID = device
	if err := m.d.Store.Update(c); err != nil {
		return err
	}
	m.mu.Lock()
	delete(m.status, id)
	m.notice = ""
	m.mu.Unlock()
	m.Refresh(ctx)
	return nil
}

// Remove deletes a connection: best-effort revoke on the server, keychain
// entry, then the saved entry.
func (m *Manager) Remove(ctx context.Context, id string) error {
	c, err := m.d.Store.Get(id)
	if err != nil {
		return err
	}
	if m.currentID() == id {
		m.closeCurrent("")
	}
	if token, err := m.d.Secrets.Get(id); err == nil && c.DeviceID != "" {
		rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		if err := m.revoke(rctx, c, token); err != nil {
			m.d.Log.Warn("revoke device", "connection", id, "err", err)
		}
		cancel()
	}
	if err := m.d.Secrets.Delete(id); err != nil {
		m.d.Log.Warn("delete keychain entry", "connection", id, "err", err)
	}
	if err := m.d.Store.Remove(id); err != nil {
		return err
	}
	m.mu.Lock()
	delete(m.status, id)
	m.mu.Unlock()
	m.d.Nav.Changed()
	return nil
}

func (m *Manager) revoke(ctx context.Context, c conns.Connection, token string) error {
	if c.Mode == conns.ModeRemote {
		return m.d.NewClient(c.URL, token).RevokeDevice(ctx, c.DeviceID)
	}
	if st, _ := m.d.Probe.Status(ctx); st.Running {
		return m.d.NewClient(st.URL, token).RevokeDevice(ctx, c.DeviceID)
	}
	bin, err := m.d.Resolver.Resolve()
	if err != nil {
		return err
	}
	return m.d.CLI(bin.Path).Revoke(ctx, c.DeviceID)
}

// Open connects to id and navigates the window to its dashboard.
func (m *Manager) Open(ctx context.Context, id string) error {
	c, err := m.d.Store.Get(id)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.openSeq++
	seq := m.openSeq
	next := ""
	if m.current == id {
		next = m.path
	}
	m.stopPollLocked()
	m.phase = PhaseOpening
	m.current = id
	m.notice = ""
	m.mu.Unlock()
	m.d.Nav.Changed()

	base, href, err := m.handoff(ctx, c, next)
	m.mu.Lock()
	if seq != m.openSeq { // a newer Open or a Switch superseded this one
		m.mu.Unlock()
		return nil
	}
	if err != nil {
		m.phase = PhaseConnections
		m.current = ""
		m.notice = err.Error()
		if errors.Is(err, api.ErrSignedOut) || errors.Is(err, secrets.ErrNotFound) {
			m.status[id] = StatusSignedOut
			m.notice = fmt.Sprintf("%s: %v", c.Name, api.ErrSignedOut)
		}
		m.mu.Unlock()
		m.d.Nav.ShowConnections()
		m.d.Nav.Changed()
		return err
	}
	m.phase = PhaseOpen
	m.currentBase = base
	if c.Mode == conns.ModeRemote {
		m.status[id] = StatusOnline
	} else {
		m.status[id] = StatusRunning
		m.local.Running, m.local.URL = true, base
	}
	m.startPollLocked(c, base)
	m.mu.Unlock()
	if err := m.d.Store.SetLast(id); err != nil {
		m.d.Log.Warn("remember last connection", "err", err)
	}
	m.d.Nav.Navigate(href)
	m.d.Nav.Changed()
	return nil
}

// handoff resolves the connection's base URL (starting the local daemon if
// needed) and mints a one-time login URL for next.
func (m *Manager) handoff(ctx context.Context, c conns.Connection, next string) (base, href string, err error) {
	token, err := m.d.Secrets.Get(c.ID)
	if err != nil {
		return "", "", err
	}
	base = c.URL
	if c.Mode == conns.ModeLocal {
		if base, err = m.ensureLocal(ctx); err != nil {
			return "", "", err
		}
	}
	hctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	h, err := m.d.NewClient(base, token).Handoff(hctx, next)
	if err != nil {
		return "", "", err
	}
	return base, joinURL(base, h.URL), nil
}

// ensureLocal returns the running daemon's URL, starting it when needed.
func (m *Manager) ensureLocal(ctx context.Context) (string, error) {
	if st, _ := m.d.Probe.Status(ctx); st.Running {
		return st.URL, nil
	}
	bin, err := m.d.Resolver.Resolve()
	if err != nil {
		return "", err
	}
	if err := m.d.CLI(bin.Path).Serve(ctx); err != nil {
		return "", err
	}
	st, err := daemon.WaitRunning(ctx, m.d.Probe, m.d.StartTimeout)
	if err != nil {
		return "", err
	}
	m.refreshLocal(ctx)
	return st.URL, nil
}

// AutoOpen opens the last connection, if any (on launch).
func (m *Manager) AutoOpen(ctx context.Context) {
	last := m.d.Store.Last()
	if last == "" {
		m.Refresh(ctx)
		return
	}
	if _, err := m.d.Store.Get(last); err != nil {
		m.Refresh(ctx)
		return
	}
	if err := m.Open(ctx, last); err != nil {
		m.d.Log.Info("auto-open failed", "connection", last, "err", err)
		m.Refresh(ctx)
	}
}

// SwitchConnection leaves the current dashboard for the connection screen.
// The local daemon keeps running.
func (m *Manager) SwitchConnection() { m.closeCurrent("") }

func (m *Manager) closeCurrent(notice string) {
	m.mu.Lock()
	m.openSeq++
	m.stopPollLocked()
	m.current, m.currentBase, m.path = "", "", ""
	m.phase = PhaseConnections
	m.notice = notice
	m.mu.Unlock()
	m.d.Nav.ShowConnections()
	m.d.Nav.Changed()
	go m.Refresh(context.Background())
}

// Retry reconnects now (the reconnect page's button). A local daemon that is
// down gets started. While it still fails, the reconnect page stays up and
// the poll keeps trying.
func (m *Manager) Retry(ctx context.Context) error {
	m.mu.Lock()
	id, phase, next, seq := m.current, m.phase, m.path, m.openSeq
	m.mu.Unlock()
	if id == "" {
		m.d.Nav.ShowConnections()
		return nil
	}
	if phase != PhaseReconnecting {
		return m.Open(ctx, id)
	}
	c, err := m.d.Store.Get(id)
	if err != nil {
		m.closeCurrent("")
		return err
	}
	base, href, err := m.handoff(ctx, c, next)
	if err != nil {
		if errors.Is(err, api.ErrSignedOut) || errors.Is(err, secrets.ErrNotFound) {
			m.mu.Lock()
			m.status[id] = StatusSignedOut
			m.mu.Unlock()
			m.closeCurrent(fmt.Sprintf("%s: %v", c.Name, api.ErrSignedOut))
		}
		return err
	}
	m.mu.Lock()
	if seq != m.openSeq || m.phase != PhaseReconnecting {
		m.mu.Unlock()
		return nil
	}
	m.phase = PhaseOpen
	m.currentBase = base
	if c.Mode == conns.ModeRemote {
		m.status[id] = StatusOnline
	} else {
		m.status[id] = StatusRunning
		m.local.Running, m.local.URL = true, base
	}
	m.mu.Unlock()
	m.d.Nav.Navigate(href)
	m.d.Nav.Changed()
	return nil
}

// StartLocal starts the daemon; if the local connection is current (or
// nothing is open), it is opened.
func (m *Manager) StartLocal(ctx context.Context) error {
	if _, err := m.ensureLocal(ctx); err != nil {
		m.d.Nav.Changed()
		return err
	}
	c, ok := m.d.Store.Local()
	cur := m.currentID()
	if ok && (cur == "" || cur == c.ID) {
		return m.Open(ctx, c.ID)
	}
	m.d.Nav.Changed()
	return nil
}

// StopLocal stops the daemon (the only way the shell ever stops it).
func (m *Manager) StopLocal(ctx context.Context) error {
	if c, ok := m.d.Store.Local(); ok && m.currentID() == c.ID {
		m.closeCurrent("Antares on this machine was stopped.")
	}
	bin, err := m.d.Resolver.Resolve()
	if err != nil {
		return err
	}
	err = m.d.CLI(bin.Path).Stop(ctx)
	m.refreshLocal(ctx)
	m.d.Nav.Changed()
	return err
}

// PagePath records the dashboard path the window is on, reported by the
// injected page script; only the current connection's origin counts. Landing
// on /login means the dashboard session ended (a revoked device ends its
// sessions): the shell re-handoffs, which either signs the window back in or
// finds the device revoked and asks to pair again.
func (m *Manager) PagePath(origin, path string) {
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		return
	}
	m.mu.Lock()
	if m.currentBase == "" || !sameOrigin(origin, m.currentBase) || m.phase != PhaseOpen {
		m.mu.Unlock()
		return
	}
	if strings.HasPrefix(path, "/login") {
		id := m.current
		recheck := time.Since(m.lastRecheck) > 30*time.Second
		if recheck {
			m.lastRecheck = time.Now()
		}
		m.mu.Unlock()
		if recheck {
			go func() { _ = m.Open(context.Background(), id) }()
		}
		return
	}
	if !strings.HasPrefix(path, "/auth/handoff") {
		m.path = path
	}
	m.mu.Unlock()
}

// CurrentOrigin is the open dashboard's origin ("" when none).
func (m *Manager) CurrentOrigin() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return originOf(m.currentBase)
}

func (m *Manager) currentID() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.current
}

func (m *Manager) stopPollLocked() {
	if m.pollCancel != nil {
		m.pollCancel()
		m.pollCancel = nil
	}
}

func (m *Manager) startPollLocked(c conns.Connection, base string) {
	ctx, cancel := context.WithCancel(context.Background())
	m.pollCancel = cancel
	seq := m.openSeq
	go m.poll(ctx, seq, c, base)
}

// poll checks health every PollEvery while open; a failure shows the
// reconnect page, and recovery re-handoffs to the same path.
func (m *Manager) poll(ctx context.Context, seq int, c conns.Connection, base string) {
	wait := m.d.PollEvery
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		hctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := m.health(hctx, c, base)
		cancel()
		if ctx.Err() != nil {
			return
		}
		m.mu.Lock()
		if seq != m.openSeq {
			m.mu.Unlock()
			return
		}
		phase := m.phase
		m.mu.Unlock()

		if err != nil {
			if phase == PhaseOpen {
				m.mu.Lock()
				m.phase = PhaseReconnecting
				m.status[c.ID] = StatusOffline
				if c.Mode == conns.ModeLocal {
					m.status[c.ID] = StatusStopped
					m.local.Running = false
				}
				m.mu.Unlock()
				m.d.Log.Info("connection lost", "connection", c.ID, "err", err)
				m.d.Nav.ShowReconnect()
				m.d.Nav.Changed()
			}
			wait = m.d.ReconnectEvery
			continue
		}
		wait = m.d.PollEvery
		if phase != PhaseReconnecting {
			// Healthy: is this device still allowed in? A revoke on the
			// server (dashboard Devices, `antares device revoke`) signs the
			// window out here rather than leaving it on a login page.
			if m.tokenRevoked(ctx, c, base) {
				m.mu.Lock()
				stale := seq != m.openSeq
				if !stale {
					m.status[c.ID] = StatusSignedOut
				}
				m.mu.Unlock()
				if !stale {
					m.closeCurrent(fmt.Sprintf("%s: %v", c.Name, api.ErrSignedOut))
				}
				return
			}
			continue
		}
		// Back: a fresh handoff to where the window was.
		m.mu.Lock()
		next := m.path
		m.mu.Unlock()
		hctx, cancel = context.WithTimeout(ctx, 15*time.Second)
		base2, href, err := m.handoff(hctx, c, next)
		cancel()
		m.mu.Lock()
		if seq != m.openSeq || ctx.Err() != nil {
			m.mu.Unlock()
			return
		}
		if err != nil {
			if errors.Is(err, api.ErrSignedOut) || errors.Is(err, secrets.ErrNotFound) {
				m.status[c.ID] = StatusSignedOut
				m.mu.Unlock()
				m.closeCurrent(fmt.Sprintf("%s: %v", c.Name, api.ErrSignedOut))
				return
			}
			m.mu.Unlock()
			wait = m.d.ReconnectEvery
			continue
		}
		m.phase = PhaseOpen
		m.currentBase = base2
		base = base2
		if c.Mode == conns.ModeRemote {
			m.status[c.ID] = StatusOnline
		} else {
			m.status[c.ID] = StatusRunning
			m.local.Running = true
		}
		m.mu.Unlock()
		m.d.Nav.Navigate(href)
		m.d.Nav.Changed()
	}
}

// tokenRevoked reports a device token the server refuses (401 on an
// authorized call). Any other failure counts as "not revoked".
func (m *Manager) tokenRevoked(ctx context.Context, c conns.Connection, base string) bool {
	token, err := m.d.Secrets.Get(c.ID)
	if err != nil {
		return errors.Is(err, secrets.ErrNotFound)
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return errors.Is(m.d.NewClient(base, token).CheckToken(cctx), api.ErrSignedOut)
}

func (m *Manager) health(ctx context.Context, c conns.Connection, base string) error {
	if c.Mode == conns.ModeLocal {
		st, err := m.d.Probe.Status(ctx)
		if err != nil {
			return err
		}
		if !st.Running {
			return errors.New("Antares is not running")
		}
		return nil
	}
	return m.d.NewClient(base, "").Health(ctx)
}

func joinURL(base, path string) string {
	u, err := url.Parse(base)
	if err != nil {
		return strings.TrimRight(base, "/") + path
	}
	return strings.TrimRight(u.Scheme+"://"+u.Host+strings.TrimRight(u.Path, "/"), "/") + path
}

func hostOf(base string) string {
	if u, err := url.Parse(base); err == nil && u.Hostname() != "" {
		return u.Hostname()
	}
	return base
}

func originOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return strings.ToLower(u.Scheme + "://" + u.Host)
}

func sameOrigin(a, b string) bool {
	oa, ob := originOf(a), originOf(b)
	return oa != "" && oa == ob
}
