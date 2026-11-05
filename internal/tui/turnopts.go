// turnopts.go holds the per-turn options the web composer sends alongside a
// message — role, reasoning effort, project folder, attachments — and the
// session commands the registry does not cover (/search, /delete).

package tui

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/enowdev/antares/internal/llm"
	"github.com/enowdev/antares/internal/tools"
)

// ---- role ---------------------------------------------------------------------

// stageRole holds a role for a conversation that has no session yet. It is
// sent with the first turn and stored against the session once one exists.
func (m *Model) stageRole(name string) tea.Cmd {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "none" || name == "default" || name == "clear" {
		m.role = ""
		m.pushSystem("Back to the general assistant.")
		return nil
	}
	if m.ag == nil || m.ag.Roles() == nil {
		m.pushSystem("Roles are not available.")
		return nil
	}
	role, ok := m.ag.Roles().Get(name)
	if !ok {
		m.pushSystem(fmt.Sprintf("No role named %q — see /roles.", name))
		return nil
	}
	m.role = role.Name
	out := fmt.Sprintf("The new conversation will run as **%s**.\n\n%s", role.Title, role.Summary)
	if role.Danger {
		out += "\n\n⚠ This role does security testing. Work only against targets you are authorized to test."
	}
	m.pushOutput(out)
	return nil
}

// ---- reasoning effort ---------------------------------------------------------

// effortValues is what the active model accepts, from the same capability
// table the dashboard's reasoning picker reads. An endpoint that advertises
// nothing gets the common three.
func (m *Model) effortValues() []string {
	if m.cfg != nil {
		p := m.cfg.Providers[m.cfg.Model.Provider]
		if c := llm.OfficialReasoning(p.Kind, p.BaseURL, m.cfg.Model.Default); len(c.Values) > 0 {
			return c.Values
		}
	}
	return []string{"low", "medium", "high"}
}

func (m *Model) cmdEffort(args string) (bool, tea.Cmd) {
	values := m.effortValues()
	if v := strings.ToLower(strings.TrimSpace(args)); v != "" {
		if v == "auto" {
			m.effort = ""
			m.setStatus("effort → auto")
			return false, nil
		}
		for _, ok := range values {
			if v == ok {
				m.effort = v
				m.setStatus("effort → " + v)
				return false, nil
			}
		}
		m.pushSystem(fmt.Sprintf("This model takes: auto, %s.", strings.Join(values, ", ")))
		return false, nil
	}
	items := []pickerItem{{id: "", label: "auto", right: "provider default"}}
	cursor := 0
	for _, v := range values {
		if v == m.effort {
			cursor = len(items)
		}
		items = append(items, pickerItem{id: v, label: v})
	}
	m.openPicker(picker{
		title:  "Reasoning effort",
		hint:   "for the next turns",
		items:  items,
		cursor: cursor,
		commit: func(m *Model, it pickerItem) {
			m.effort = it.id
			m.setStatus("effort → " + firstNon(it.id, "auto"))
		},
	})
	return false, nil
}

// ---- project ------------------------------------------------------------------

// cmdProject binds a folder to the conversation. As in the web's project
// picker, the binding is sent only with a session's first turn; after that
// the session carries it and cannot be re-bound.
func (m *Model) cmdProject(args string) (bool, tea.Cmd) {
	args = strings.TrimSpace(args)
	if args == "" {
		switch {
		case m.projectDir != "" && m.sessionID != "":
			m.pushSystem("This session is bound to " + m.projectDir + ".")
		case m.projectDir != "":
			m.pushSystem("The new session will be bound to " + m.projectDir + ". /project clear to unbind.")
		default:
			m.pushSystem("No project is bound. /project <dir> binds one to a new session.")
		}
		return false, nil
	}
	if m.sessionID != "" {
		m.pushSystem("A project is fixed when a session starts. Run /new, then /project " + args + ".")
		return false, nil
	}
	if args == "clear" || args == "none" {
		m.projectDir = ""
		m.setStatus("project cleared")
		return false, nil
	}
	dir, err := resolveDir(args)
	if err != nil {
		m.pushSystem("Project: " + err.Error())
		return false, nil
	}
	m.projectDir = dir
	m.pushSystem("The new session will be bound to " + dir + ": writes stay inside it, and its AGENTS.md and README are read in.")
	return false, nil
}

// resolveDir expands ~ and makes a path absolute, requiring a directory.
func resolveDir(p string) (string, error) {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		p = filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if !st.IsDir() {
		return "", fmt.Errorf("%s is not a directory", abs)
	}
	return abs, nil
}

// ---- attachments ----------------------------------------------------------------

// attachment is one file waiting to go with the next message. An image is
// sent to the model inline; anything else is copied where read_document can
// reach it and named in the message, as the web's upload does.
type attachment struct {
	name  string
	path  string // uploads-relative path for a document
	image bool
	part  llm.Part
}

// attachedMsg carries a prepared attachment back to the update loop.
type attachedMsg struct {
	att attachment
	err error
}

// attachMaxBytes matches the server's upload cap.
const attachMaxBytes = 25 << 20

var unsafeUploadName = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func (m *Model) cmdAttach(args string) (bool, tea.Cmd) {
	args = strings.TrimSpace(args)
	switch args {
	case "":
		if len(m.attachments) == 0 {
			m.pushSystem("Nothing attached. /attach <path> adds a file or image to the next message.")
			return false, nil
		}
		names := make([]string, len(m.attachments))
		for i, a := range m.attachments {
			names[i] = a.name
		}
		m.pushSystem("Attached to the next message: " + strings.Join(names, ", ") + ". /attach clear drops them.")
		return false, nil
	case "clear":
		m.attachments = nil
		m.setStatus("attachments cleared")
		return false, nil
	}
	sub := m.sessionID
	return false, func() tea.Msg {
		att, err := prepareAttachment(args, sub)
		return attachedMsg{att: att, err: err}
	}
}

// prepareAttachment reads a local file into an attachment. Images become
// inline image parts, the shape the server decodes uploads into; other files
// are copied under the uploads directory (per session, "misc" before one
// exists) so read_document can open them.
func prepareAttachment(path, sessionID string) (attachment, error) {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, strings.TrimPrefix(path, "~"))
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return attachment{}, err
	}
	if len(data) > attachMaxBytes {
		return attachment{}, fmt.Errorf("%s is too large (max 25 MB)", filepath.Base(path))
	}
	name := sanitizeUploadName(filepath.Base(path))
	if name == "" {
		return attachment{}, fmt.Errorf("%s has no usable file name", path)
	}
	if mime := imageMime(path, data); mime != "" {
		return attachment{
			name:  name,
			image: true,
			part:  llm.Part{Type: "image", MimeType: mime, Data: base64.StdEncoding.EncodeToString(data)},
		}, nil
	}
	sub := sanitizeUploadName(sessionID)
	if sub == "" {
		sub = "misc"
	}
	dir := filepath.Join(tools.UploadsDir(), sub)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return attachment{}, err
	}
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		return attachment{}, err
	}
	return attachment{name: name, path: filepath.Join(sub, name)}, nil
}

// imageMime reports the image type of a file the model can take inline, or ""
// when it is not one.
func imageMime(path string, data []byte) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	}
	if t := http.DetectContentType(data); strings.HasPrefix(t, "image/") && t != "image/svg+xml" {
		return t
	}
	return ""
}

// sanitizeUploadName mirrors the server's upload naming.
func sanitizeUploadName(s string) string {
	s = filepath.Base(strings.TrimSpace(s))
	s = unsafeUploadName.ReplaceAllString(s, "_")
	s = strings.Trim(s, "._-")
	if len(s) > 128 {
		s = s[len(s)-128:]
	}
	return s
}

// buildMessage folds attachments into what is sent: image parts ride along as
// Images, and documents are listed in the text with the same note the web chat
// writes, so the model knows to read them.
func buildMessage(text string, atts []attachment) (string, []llm.Part) {
	var images []llm.Part
	var docs []string
	for _, a := range atts {
		if a.image {
			images = append(images, a.part)
			continue
		}
		docs = append(docs, fmt.Sprintf("- %s (path: %s)", a.name, a.path))
	}
	if len(docs) == 0 {
		return text, images
	}
	note := "Attached file(s) — read each with the read_document tool before answering:\n" + strings.Join(docs, "\n")
	if strings.TrimSpace(text) == "" {
		return note, images
	}
	return text + "\n\n" + note, images
}

// ---- search -------------------------------------------------------------------

type searchResultMsg struct {
	query string
	items []pickerItem
	err   error
}

// cmdSearch finds past messages and opens a picker over the sessions they are
// in; choosing one resumes it.
func (m *Model) cmdSearch(args string) (bool, tea.Cmd) {
	q := strings.TrimSpace(args)
	if q == "" {
		m.pushSystem("Usage: /search <text>")
		return false, nil
	}
	if m.db == nil {
		m.pushSystem("No store available.")
		return false, nil
	}
	db := m.db
	m.setStatus("searching…")
	return false, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		hits, err := db.SearchMessages(ctx, q, 50)
		if err != nil {
			return searchResultMsg{query: q, err: err}
		}
		// One row per session, best hit first — the store ranks them.
		var items []pickerItem
		seen := map[string]bool{}
		for _, h := range hits {
			if seen[h.SessionID] {
				continue
			}
			seen[h.SessionID] = true
			title := firstNon(h.SessionTitle, "(untitled)")
			items = append(items, pickerItem{
				id:    h.SessionID,
				label: truncate(title, 30) + " — " + previewText(h.Snippet, 50),
				right: h.CreatedAt.Format("02 Jan"),
			})
		}
		return searchResultMsg{query: q, items: items}
	}
}

func (m *Model) showSearchResults(msg searchResultMsg) {
	m.setStatus("")
	if msg.err != nil {
		m.pushSystem("Search failed: " + msg.err.Error())
		return
	}
	if len(msg.items) == 0 {
		m.pushSystem("No messages match " + msg.query + ".")
		return
	}
	m.openPicker(picker{
		title: "Resume a conversation",
		hint:  fmt.Sprintf("%d match “%s”", len(msg.items), msg.query),
		items: msg.items,
		commit: func(m *Model, it pickerItem) {
			if m.busy {
				m.setStatus("still working — /stop first")
				return
			}
			m.after = m.loadSessionCmd(it.id, "resumed "+shortID(it.id))
		},
	})
}

// ---- delete -------------------------------------------------------------------

// pendingConfirm is a yes/no staged inline, confirmed with "y" on an empty
// composer like a staged /undo.
type pendingConfirm struct {
	onYes func(m *Model) tea.Cmd
	// cancelled is shown when anything but "y" arrives.
	cancelled string
}

type sessionDeletedMsg struct {
	id  string
	err error
}

func (m *Model) cmdDelete(string) (bool, tea.Cmd) {
	if m.sessionID == "" {
		m.pushSystem("There is no saved conversation to delete.")
		return false, nil
	}
	if m.db == nil {
		m.pushSystem("No store available.")
		return false, nil
	}
	if m.busy {
		m.setStatus("still working — /stop first")
		return false, nil
	}
	id := m.sessionID
	label := firstNon(m.title, shortID(id))
	m.confirm = &pendingConfirm{
		cancelled: "Delete cancelled.",
		onYes: func(m *Model) tea.Cmd {
			db := m.db
			return func() tea.Msg {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				return sessionDeletedMsg{id: id, err: db.DeleteSession(ctx, id)}
			}
		},
	}
	m.pushSystem("Delete “" + label + "” and its whole history? This cannot be undone.\nPress y to confirm, any other key to cancel.")
	return false, nil
}
