package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"github.com/enowdev/antares/internal/agent"
	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/llm"
	"github.com/enowdev/antares/internal/store"
	"github.com/enowdev/antares/internal/tools"
)

// askOptions is a parsed `antares ask` invocation.
type askOptions struct {
	Session string
	New     bool
	Role    string
	Model   string
	Effort  string
	Project string
	Attach  []string
	Quiet   bool
	Prompt  string
}

const askUsage = `usage: antares ask [--session <id>|--new] [--role r] [--model m] [--effort e]
                   [--project dir] [--attach path]... [-q] ["prompt"]

The prompt comes from the arguments, from stdin, or both (stdin is appended):
  antares ask "what changed in go 1.26?"
  git diff | antares ask "review this"
  antares ask --session <id> "and the next step?"`

// parseAskArgs reads flags anywhere on the line; everything else, or everything
// after "--", is the prompt.
func parseAskArgs(args []string) (askOptions, error) {
	var o askOptions
	var words []string
	value := func(i *int, flag string) (string, error) {
		if *i+1 >= len(args) {
			return "", fmt.Errorf("%s needs a value", flag)
		}
		*i++
		return args[*i], nil
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		name, inline, hasInline := strings.Cut(a, "=")
		if !strings.HasPrefix(a, "-") || a == "-" {
			words = append(words, a)
			continue
		}
		if a == "--" {
			words = append(words, args[i+1:]...)
			break
		}
		get := func() (string, error) {
			if hasInline {
				return inline, nil
			}
			return value(&i, name)
		}
		var err error
		switch name {
		case "--session", "-s":
			o.Session, err = get()
		case "--new", "-n":
			o.New = true
		case "--role", "-r":
			o.Role, err = get()
		case "--model", "-m":
			o.Model, err = get()
		case "--effort", "-e":
			o.Effort, err = get()
		case "--project", "-p":
			o.Project, err = get()
		case "--attach", "-a":
			var v string
			v, err = get()
			o.Attach = append(o.Attach, v)
		case "--quiet", "-q":
			o.Quiet = true
		case "--help", "-h":
			return o, errAskHelp
		default:
			return o, fmt.Errorf("unknown ask option %q", a)
		}
		if err != nil {
			return o, err
		}
	}
	if o.New && o.Session != "" {
		return o, errors.New("--new and --session cannot be combined")
	}
	o.Prompt = strings.TrimSpace(strings.Join(words, " "))
	return o, nil
}

var errAskHelp = errors.New("help requested")

// combinePrompt joins the argument prompt with piped input, so
// `cat notes.md | antares ask "summarise"` sends both.
func combinePrompt(arg, piped string) string {
	arg, piped = strings.TrimSpace(arg), strings.TrimSpace(piped)
	switch {
	case arg == "":
		return piped
	case piped == "":
		return arg
	default:
		return arg + "\n\n" + piped
	}
}

// stdinIsPiped reports whether stdin is a pipe or a redirected file — not a
// terminal, and not an empty character device such as /dev/null.
func stdinIsPiped() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	m := fi.Mode()
	return m&os.ModeNamedPipe != 0 || m.IsRegular()
}

// askNoAnswer is what ask_user gets back in a one-shot run. Answering (rather
// than aborting) lets the turn finish with whatever it can do, and the wording
// forbids treating the silence as consent to anything the question was about.
const askNoAnswer = "No answer is possible: the user is running a non-interactive one-shot " +
	"command (antares ask) and cannot reply. Do not take any action that depended on this " +
	"answer and do not assume a default for it. Finish what you can without it, then say " +
	"plainly what you still need to know."

func cmdAsk(args []string) error {
	opts, err := parseAskArgs(args)
	if errors.Is(err, errAskHelp) {
		fmt.Println(askUsage)
		return nil
	}
	if err != nil {
		return fmt.Errorf("%w\n%s", err, askUsage)
	}
	if stdinIsPiped() {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("reading stdin: %w", err)
		}
		opts.Prompt = combinePrompt(opts.Prompt, string(b))
	}
	if opts.Prompt == "" && len(opts.Attach) == 0 {
		return fmt.Errorf("no prompt: pass it as an argument or on stdin\n%s", askUsage)
	}
	if opts.Project != "" {
		abs, err := filepath.Abs(config.Expand(opts.Project))
		if err != nil {
			return err
		}
		if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
			return fmt.Errorf("--project %s is not a directory", opts.Project)
		}
		opts.Project = abs
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	quietBootstrapLogs()
	rt, err := bootstrap(ctx)
	if err != nil {
		return err
	}
	defer rt.close()
	if needsSetup(rt.cfg) {
		return errors.New("Antares is not configured yet — run `antares setup` first")
	}

	sessionID := ""
	if opts.Session != "" {
		sessionID, err = resolveSessionID(ctx, rt.db, opts.Session)
		if err != nil {
			return err
		}
	}

	images, note, err := prepareAttachments(opts.Attach)
	if err != nil {
		return err
	}
	message := opts.Prompt
	if note != "" {
		message = combinePrompt(message, note)
	}

	notice := func(format string, a ...any) {
		if !opts.Quiet {
			fmt.Fprintf(os.Stderr, format+"\n", a...)
		}
	}
	if opts.Project != "" && sessionID != "" {
		notice("· --project is ignored when continuing a session; it keeps the folder it started with")
	}
	if sessionID != "" && opts.Role != "" {
		_ = rt.db.SetKV(ctx, "role:"+sessionID, opts.Role)
	}

	ag := rt.agent
	var printed, endsNewline bool
	req := agent.Request{
		SessionID:       sessionID,
		Message:         message,
		Images:          images,
		Role:            opts.Role,
		Model:           opts.Model,
		ReasoningEffort: opts.Effort,
		ProjectDir:      opts.Project,
		Platform:        "cli",
	}
	res, runErr := ag.Run(ctx, req, func(e agent.Event) error {
		switch e.Type {
		case agent.EventSession:
			if e.ID != "" && sessionID == "" {
				sessionID = e.ID
				// Remember the picked role against the new session, as the
				// dashboard does, so `--session` continues as the same specialist.
				if opts.Role != "" {
					_ = rt.db.SetKV(context.Background(), "role:"+e.ID, opts.Role)
				}
			}
		case agent.EventText:
			if e.Delta != "" {
				fmt.Print(e.Delta)
				printed = true
				endsNewline = strings.HasSuffix(e.Delta, "\n")
			}
		case agent.EventToolCall:
			notice("→ %s %s", e.Name, oneLine(e.Arguments, 100))
		case agent.EventToolResult:
			if e.IsError {
				notice("✗ %s: %s", e.Name, oneLine(e.Content, 160))
			}
		case agent.EventNotice:
			notice("· %s", e.Message)
		case agent.EventReset:
			if printed && !endsNewline {
				fmt.Println()
				endsNewline = true
			}
			notice("· retrying the turn; the partial reply above was discarded")
		case agent.EventApproval:
			// Nobody can click approve here. Refuse at once rather than leave
			// the turn waiting out the five-minute approval timeout.
			ag.ResolveApproval(e.ID, false)
			fmt.Fprintf(os.Stderr, "✗ refused %s: it needs approval, which a one-shot run cannot give "+
				"(tools.approval_mode is \"prompt\")\n", e.Name)
		case agent.EventAsk:
			ag.ResolveAsk(e.ID, askNoAnswer)
			fmt.Fprintln(os.Stderr, "✗ the agent asked a question; a one-shot run cannot answer, so it was told to continue without one")
		}
		return nil
	})
	if !printed && res != nil && strings.TrimSpace(res.Reply) != "" {
		fmt.Print(res.Reply)
		printed = true
		endsNewline = strings.HasSuffix(res.Reply, "\n")
	}
	if printed && !endsNewline {
		fmt.Println()
	}
	if res != nil && res.SessionID != "" {
		sessionID = res.SessionID
	}
	if sessionID != "" {
		fmt.Fprintf(os.Stderr, "session: %s\n", sessionID)
	}
	if runErr != nil {
		if errors.Is(runErr, context.Canceled) {
			return errors.New("interrupted")
		}
		return runErr
	}
	return nil
}

func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len([]rune(s)) > max {
		return string([]rune(s)[:max-1]) + "…"
	}
	return s
}

const attachMaxBytes = 25 << 20

var imageMIME = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
	".gif": "image/gif", ".webp": "image/webp",
}

var unsafeAttachName = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

// prepareAttachments mirrors the dashboard: images go to the model inline;
// anything else is copied into the uploads directory and named in the message
// so the agent reads it with read_document.
func prepareAttachments(paths []string) ([]llm.Part, string, error) {
	if len(paths) == 0 {
		return nil, "", nil
	}
	var images []llm.Part
	var docs []string
	dir := ""
	for _, p := range paths {
		data, err := os.ReadFile(config.Expand(p))
		if err != nil {
			return nil, "", fmt.Errorf("--attach %s: %w", p, err)
		}
		if len(data) > attachMaxBytes {
			return nil, "", fmt.Errorf("--attach %s: larger than 25 MB", p)
		}
		if mime, ok := imageMIME[strings.ToLower(filepath.Ext(p))]; ok {
			images = append(images, llm.Part{Type: "image", MimeType: mime, Data: base64.StdEncoding.EncodeToString(data)})
			continue
		}
		if dir == "" {
			dir = "cli-" + randHex(6)
			if err := os.MkdirAll(filepath.Join(tools.UploadsDir(), dir), 0o755); err != nil {
				return nil, "", err
			}
		}
		name := attachName(p)
		if err := os.WriteFile(filepath.Join(tools.UploadsDir(), dir, name), data, 0o644); err != nil {
			return nil, "", err
		}
		docs = append(docs, fmt.Sprintf("- %s (path: %s)", name, filepath.Join(dir, name)))
	}
	note := ""
	if len(docs) > 0 {
		note = "Attached file(s) — read each with the read_document tool before answering:\n" + strings.Join(docs, "\n")
	}
	return images, note, nil
}

func attachName(p string) string {
	s := unsafeAttachName.ReplaceAllString(filepath.Base(p), "_")
	s = strings.Trim(s, "._-")
	if s == "" {
		s = "attachment"
	}
	if len(s) > 128 {
		s = s[len(s)-128:]
	}
	return s
}

// resolveSessionID accepts a full id or an unambiguous prefix of one, since
// listings shorten ids.
func resolveSessionID(ctx context.Context, db store.Store, id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", errors.New("a session id is required")
	}
	if s, err := db.GetSession(ctx, id); err == nil && s != nil {
		return s.ID, nil
	}
	list, _, err := db.ListSessions(ctx, store.SessionFilter{Limit: 100000})
	if err != nil {
		return "", err
	}
	return matchSessionPrefix(list, id)
}

func matchSessionPrefix(list []store.Session, prefix string) (string, error) {
	var hits []string
	for _, s := range list {
		if strings.HasPrefix(s.ID, prefix) {
			hits = append(hits, s.ID)
		}
	}
	switch len(hits) {
	case 0:
		return "", fmt.Errorf("no session matches %q", prefix)
	case 1:
		return hits[0], nil
	default:
		return "", fmt.Errorf("%q matches %d sessions — give more of the id", prefix, len(hits))
	}
}
