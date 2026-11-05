package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/enowdev/antares/internal/config"
)

// cmdSoul reads and writes SOUL.md, the agent's identity — the same file the
// dashboard's Soul page edits. It is read fresh when each turn's prompt is
// built, so a change applies to the next message without a restart.
func cmdSoul(args []string) error {
	sub := "show"
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "show", "cat":
		fmt.Println(config.Soul())
		if config.SoulIsUnset() {
			fmt.Fprintln(os.Stderr, "note: no identity is set yet; the agent will ask for one in its first conversation")
		}
		return nil
	case "path":
		fmt.Println(config.SoulPath())
		return nil
	case "edit":
		return editSoul()
	case "set":
		if len(args) < 2 {
			return errors.New("usage: antares soul set <file>   (use - to read stdin)")
		}
		body, err := readFileOrStdin(args[1])
		if err != nil {
			return err
		}
		if strings.TrimSpace(body) == "" {
			return errors.New("the new soul is empty — use `antares soul reset` to go back to the default")
		}
		if err := config.SaveSoul(body); err != nil {
			return err
		}
		fmt.Printf("Saved %s\n", config.SoulPath())
		return nil
	case "reset":
		if err := config.SaveSoul(""); err != nil {
			return err
		}
		fmt.Println("Reset to the default; the agent will ask who it is in its next conversation.")
		return nil
	default:
		return fmt.Errorf("unknown soul command %q (use show|edit|set <file>|reset|path)", sub)
	}
}

func readFileOrStdin(path string) (string, error) {
	if path == "-" {
		b, err := io.ReadAll(os.Stdin)
		return string(b), err
	}
	b, err := os.ReadFile(config.Expand(path))
	return string(b), err
}

// editSoul opens SOUL.md in $VISUAL or $EDITOR, creating it from the default
// first so the editor never opens an empty buffer.
func editSoul() error {
	path := config.SoulPath()
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if err := config.SaveSoul(""); err != nil {
			return err
		}
	}
	before, _ := os.ReadFile(path)
	editor := editorCommand(os.Getenv("VISUAL"), os.Getenv("EDITOR"), runtime.GOOS)
	fields := strings.Fields(editor)
	cmd := exec.Command(fields[0], append(fields[1:], path)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", editor, err)
	}
	if after, _ := os.ReadFile(path); string(after) == string(before) {
		fmt.Println("Unchanged.")
		return nil
	}
	fmt.Printf("Saved %s — the next message uses it\n", path)
	return nil
}

// editorCommand picks the editor the way most Unix tools do: $VISUAL, then
// $EDITOR, then a platform default.
func editorCommand(visual, editor, goos string) string {
	for _, e := range []string{visual, editor} {
		if strings.TrimSpace(e) != "" {
			return strings.TrimSpace(e)
		}
	}
	if goos == "windows" {
		return "notepad"
	}
	return "vi"
}
