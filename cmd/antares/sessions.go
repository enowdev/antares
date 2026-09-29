package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/store"
	"golang.org/x/term"
)

// splitFlags separates recognised flags from positional words. Boolean flags
// are listed in bools; flags taking a value in values. Unknown dashes are an
// error so a typo never silently becomes part of a title.
func splitFlags(args []string, bools, values []string) (map[string]string, []string, error) {
	flags := map[string]string{}
	var words []string
	isIn := func(list []string, s string) bool {
		for _, x := range list {
			if x == s {
				return true
			}
		}
		return false
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			words = append(words, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			words = append(words, a)
			continue
		}
		name, inline, hasInline := strings.Cut(a, "=")
		switch {
		case isIn(bools, name) && !hasInline:
			flags[name] = "true"
		case isIn(values, name):
			if hasInline {
				flags[name] = inline
				continue
			}
			if i+1 >= len(args) {
				return nil, nil, fmt.Errorf("%s needs a value", name)
			}
			i++
			flags[name] = args[i]
		default:
			return nil, nil, fmt.Errorf("unknown option %q", a)
		}
	}
	return flags, words, nil
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

const sessionsUsage = `usage:
  antares sessions [list] [--limit N] [--json]
  antares sessions show <id> [--json]
  antares sessions rename <id> <title>
  antares sessions delete <id>... [--yes]
  antares sessions export <id> [--md|--json] [-o file]`

// cmdSessions manages conversations from the shell. Ids may be given as any
// unambiguous prefix.
func cmdSessions(args []string) error {
	sub := "list"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub, args = args[0], args[1:]
	}
	ctx := context.Background()
	env, err := openCLIEnv(ctx)
	if err != nil {
		return err
	}
	defer env.close()
	db := env.db

	switch sub {
	case "list", "ls":
		flags, _, err := splitFlags(args, []string{"--json"}, []string{"--limit", "-n"})
		if err != nil {
			return err
		}
		limit := 20
		for _, k := range []string{"--limit", "-n"} {
			if v, ok := flags[k]; ok {
				if limit, err = strconv.Atoi(v); err != nil || limit <= 0 {
					return fmt.Errorf("%s wants a positive number", k)
				}
			}
		}
		list, total, err := db.ListSessions(ctx, store.SessionFilter{
			Limit: limit, ExcludePlatforms: []string{"subagent", "background"},
		})
		if err != nil {
			return err
		}
		if flags["--json"] != "" {
			if list == nil {
				list = []store.Session{}
			}
			return printJSON(map[string]any{"sessions": list, "total": total})
		}
		if len(list) == 0 {
			fmt.Println("No sessions yet.")
			return nil
		}
		writeSessionTable(os.Stdout, list)
		return nil

	case "show", "view":
		flags, words, err := splitFlags(args, []string{"--json"}, nil)
		if err != nil {
			return err
		}
		sess, msgs, err := loadSession(ctx, db, words)
		if err != nil {
			return err
		}
		if flags["--json"] != "" {
			return printJSON(map[string]any{"session": sess, "messages": msgs})
		}
		fmt.Print(renderForTerminal(sessionMarkdown(sess, msgs), stdoutIsTTY()))
		return nil

	case "rename", "title":
		if len(args) < 2 {
			return errors.New("usage: antares sessions rename <id> <title>")
		}
		id, err := resolveSessionID(ctx, db, args[0])
		if err != nil {
			return err
		}
		sess, err := db.GetSession(ctx, id)
		if err != nil {
			return err
		}
		sess.Title = strings.TrimSpace(strings.Join(args[1:], " "))
		if err := db.UpdateSession(ctx, sess); err != nil {
			return err
		}
		fmt.Printf("Renamed %s to %q\n", sess.ID, sess.Title)
		return nil

	case "delete", "rm":
		flags, words, err := splitFlags(args, []string{"--yes", "-y"}, nil)
		if err != nil {
			return err
		}
		if len(words) == 0 {
			return errors.New("usage: antares sessions delete <id>... [--yes]")
		}
		var ids []string
		for _, w := range words {
			id, err := resolveSessionID(ctx, db, w)
			if err != nil {
				return err
			}
			ids = append(ids, id)
		}
		if flags["--yes"] == "" && flags["-y"] == "" {
			if !term.IsTerminal(int(os.Stdin.Fd())) {
				return errors.New("refusing to delete without --yes when there is no terminal to confirm on")
			}
			fmt.Printf("Delete %d session(s): %s? [y/N] ", len(ids), strings.Join(ids, ", "))
			line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
			if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
				fmt.Println("Nothing deleted.")
				return nil
			}
		}
		for _, id := range ids {
			if err := db.DeleteSession(ctx, id); err != nil {
				return err
			}
			fmt.Printf("Deleted %s\n", id)
		}
		return nil

	case "export":
		flags, words, err := splitFlags(args, []string{"--md", "--markdown", "--json"}, []string{"-o", "--output"})
		if err != nil {
			return err
		}
		sess, msgs, err := loadSession(ctx, db, words)
		if err != nil {
			return err
		}
		var body []byte
		if flags["--json"] != "" {
			body, err = json.MarshalIndent(map[string]any{
				"session": sess, "messages": msgs, "exported_at": time.Now(),
			}, "", "  ")
			if err != nil {
				return err
			}
			body = append(body, '\n')
		} else {
			body = []byte(sessionMarkdown(sess, msgs))
		}
		out := flags["-o"]
		if out == "" {
			out = flags["--output"]
		}
		if out == "" || out == "-" {
			_, err = os.Stdout.Write(body)
			return err
		}
		if err := os.WriteFile(config.Expand(out), body, 0o644); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "Exported %d message(s) to %s\n", len(msgs), out)
		return nil

	case "help", "-h", "--help":
		fmt.Println(sessionsUsage)
		return nil
	default:
		return fmt.Errorf("unknown sessions command %q\n%s", sub, sessionsUsage)
	}
}

func loadSession(ctx context.Context, db store.Store, words []string) (*store.Session, []store.Message, error) {
	if len(words) != 1 {
		return nil, nil, errors.New("give exactly one session id")
	}
	id, err := resolveSessionID(ctx, db, words[0])
	if err != nil {
		return nil, nil, err
	}
	sess, err := db.GetSession(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	msgs, err := db.ListMessages(ctx, id, 0, 0)
	if err != nil {
		return nil, nil, err
	}
	return sess, msgs, nil
}

func writeSessionTable(w io.Writer, list []store.Session) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tUPDATED\tMSGS\tPLATFORM\tTITLE")
	for _, s := range list {
		title := s.Title
		if strings.TrimSpace(title) == "" {
			title = "(untitled)"
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\n", s.ID, s.UpdatedAt.Local().Format("2006-01-02 15:04"),
			s.MessageCount, orDash(s.Platform), oneLine(title, 60))
	}
	_ = tw.Flush()
}

// sessionMarkdown renders a transcript in the layout /export writes. Hidden
// messages (background context injected into a turn) are skipped, and tool
// results are cut to one line.
func sessionMarkdown(sess *store.Session, msgs []store.Message) string {
	var b strings.Builder
	title := sess.Title
	if strings.TrimSpace(title) == "" {
		title = "(untitled)"
	}
	fmt.Fprintf(&b, "# %s\n\n", title)
	fmt.Fprintf(&b, "%s · %s · %s\n\n", sess.ID, orDash(sess.Model), sess.CreatedAt.Local().Format("2 January 2006, 15:04"))
	for _, m := range msgs {
		if m.Hidden {
			continue
		}
		switch m.Role {
		case store.RoleUser:
			fmt.Fprintf(&b, "## You\n\n%s\n\n", strings.TrimSpace(m.Content))
		case store.RoleAssistant:
			if strings.TrimSpace(m.Content) != "" {
				fmt.Fprintf(&b, "## Antares\n\n%s\n\n", strings.TrimSpace(m.Content))
			}
		case store.RoleTool:
			fmt.Fprintf(&b, "> `%s` → %s\n\n", m.ToolName, oneLine(m.Content, 120))
		}
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

const skillsUsage = `usage:
  antares skills [list] [--json [--all]] List installed skills (--all adds the bundled library)
  antares skills show <name>             Print a skill's instructions
  antares skills enable|disable <name>   Turn a skill on or off
  antares skills search [words]          Search the skill hub
  antares skills install <id>            Install from the hub`

// cmdSkillsCLI adds what the shell needs beyond the /skills command: JSON,
// reading one skill, and toggling. Everything else goes to the registry.
func cmdSkillsCLI(args []string) error {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "", "list", "ls", "--json":
		flags, words, err := splitFlags(args, []string{"--json", "--all"}, nil)
		if err != nil {
			return err
		}
		if len(words) > 0 && (words[0] == "list" || words[0] == "ls") {
			words = words[1:]
		}
		if flags["--json"] == "" {
			return runRegistryCommand("skills", strings.Join(words, " "))
		}
		env, err := openCLIEnv(context.Background())
		if err != nil {
			return err
		}
		defer env.close()
		// The bundled security library holds thousands of skills; --all
		// includes it, as a /skills filter would.
		list := env.skillManager().Everyday()
		if flags["--all"] != "" {
			list = env.skillManager().List()
		}
		return printJSON(map[string]any{"skills": list})

	case "show", "cat":
		if len(args) != 2 {
			return errors.New("usage: antares skills show <name>")
		}
		env, err := openCLIEnv(context.Background())
		if err != nil {
			return err
		}
		defer env.close()
		sk, ok := env.skillManager().Get(args[1])
		if !ok {
			return fmt.Errorf("no skill named %q", args[1])
		}
		state := "enabled"
		if !sk.Enabled {
			state = "disabled"
		}
		fmt.Fprintf(os.Stderr, "%s — %s\n%s · %s\n\n", sk.Name, sk.Description, sk.Path, state)
		fmt.Println(strings.TrimSpace(sk.Body))
		return nil

	case "enable", "disable", "on", "off":
		if len(args) != 2 {
			return fmt.Errorf("usage: antares skills %s <name>", sub)
		}
		enable := sub == "enable" || sub == "on"
		env, err := openCLIEnv(context.Background())
		if err != nil {
			return err
		}
		defer env.close()
		if _, ok := env.skillManager().Get(args[1]); !ok {
			return fmt.Errorf("no skill named %q", args[1])
		}
		if _, err := config.SetSkillEnabled(args[1], enable); err != nil {
			return err
		}
		word := "Disabled"
		if enable {
			word = "Enabled"
		}
		fmt.Printf("%s %s\n", word, args[1])
		noteDaemonRestart()
		return nil

	case "help", "-h", "--help":
		fmt.Println(skillsUsage)
		return nil
	default:
		return runRegistryCommand("skills", strings.Join(args, " "))
	}
}

const memoryUsage = `usage:
  antares memory [list] [--json]    Recent memories
  antares memory search <query>     Search memories
  antares memory add <text>         Save one ("key: text" sets a key)
  antares memory forget <key|id>    Delete one`

// cmdMemoryCLI maps shell verbs onto the /memory, /remember and /forget
// commands, adding only JSON output.
func cmdMemoryCLI(args []string) error {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	rest := ""
	if len(args) > 1 {
		rest = strings.Join(args[1:], " ")
	}
	switch sub {
	case "", "list", "ls", "--json":
		flags, _, err := splitFlags(args, []string{"--json"}, []string{"--limit", "-n"})
		if err != nil {
			return err
		}
		if flags["--json"] == "" {
			return runRegistryCommand("memory", "")
		}
		limit := 200
		if v := flags["--limit"] + flags["-n"]; v != "" {
			if limit, err = strconv.Atoi(v); err != nil || limit <= 0 {
				return errors.New("--limit wants a positive number")
			}
		}
		env, err := openCLIEnv(context.Background())
		if err != nil {
			return err
		}
		defer env.close()
		items, err := env.db.ListMemories(context.Background(), "", "", limit)
		if err != nil {
			return err
		}
		if items == nil {
			items = []store.Memory{}
		}
		return printJSON(map[string]any{"memories": items})
	case "search", "find":
		if rest == "" {
			return errors.New("usage: antares memory search <query>")
		}
		return runRegistryCommand("memory", rest)
	case "add", "remember":
		return runRegistryCommand("remember", rest)
	case "forget", "rm", "delete":
		return runRegistryCommand("forget", rest)
	case "help", "-h", "--help":
		fmt.Println(memoryUsage)
		return nil
	default:
		// Anything else is a search, as `/memory <query>` is.
		return runRegistryCommand("memory", strings.Join(args, " "))
	}
}
