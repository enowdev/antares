package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/migrate"
	"github.com/enowdev/antares/internal/rag"
	"github.com/enowdev/antares/internal/store"
	"golang.org/x/term"
)

const migrateUsage = `usage:
  antares migrate                      list agents found on this machine
  antares migrate <source> [--root DIR] [--profile P] [--dry-run]
                  [--only provider,skill,…] [--yes] [--conflict skip|replace|rename|append]
  antares migrate backups              past migrations (undo handles)
  antares migrate undo <backup-dir>    reverse one migration`

func cmdMigrate(args []string) error {
	return runMigrate(context.Background(), args, os.Stdout, term.IsTerminal(int(os.Stdin.Fd())))
}

func runMigrate(ctx context.Context, args []string, out io.Writer, interactive bool) error {
	if len(args) == 0 || args[0] == "list" || args[0] == "ls" {
		return migrateList(ctx, out)
	}
	switch args[0] {
	case "help", "-h", "--help":
		fmt.Fprintln(out, migrateUsage)
		return nil
	case "backups":
		return migrateBackups(out)
	case "undo":
		if len(args) < 2 {
			return errors.New("usage: antares migrate undo <backup-dir>")
		}
		return migrateUndo(ctx, args[1], out)
	}
	if strings.HasPrefix(args[0], "-") {
		return errors.New(migrateUsage)
	}
	return migrateRun(ctx, args[0], args[1:], out, interactive)
}

func migrateList(ctx context.Context, out io.Writer) error {
	found := 0
	for _, s := range migrate.DetectAll(ctx) {
		if len(s.Detected) == 0 {
			fmt.Fprintf(out, "  %-10s %-16s not found\n", s.ID, s.Name)
			continue
		}
		for _, d := range s.Detected {
			found++
			name := s.ID
			if d.Profile != "" {
				name += " --profile " + d.Profile
			}
			running := ""
			if d.Running {
				running = "  (running)"
			}
			fmt.Fprintf(out, "• %-10s %-16s %s%s\n    %s\n", s.ID, s.Name, d.Root, running, d.Summary)
			if d.Profile != "" {
				fmt.Fprintf(out, "    import with: antares migrate %s\n", name)
			}
		}
	}
	if found == 0 {
		fmt.Fprintln(out, "\nNo other agents found. Point at one with: antares migrate <source> --root DIR")
	} else {
		fmt.Fprintln(out, "\nPreview an import with: antares migrate <source> --dry-run")
	}
	return nil
}

func migrateBackups(out io.Writer) error {
	list := migrate.ListBackups()
	if len(list) == 0 {
		fmt.Fprintln(out, "No migrations yet.")
		return nil
	}
	for _, b := range list {
		state := ""
		if b.Undone {
			state = "  (undone)"
		}
		fmt.Fprintf(out, "%-44s %-9s %3d items  %s%s\n", b.Name, b.Source, b.Applied, b.CreatedAt.Local().Format("2 Jan 2006 15:04"), state)
	}
	return nil
}

// migrateDeps opens the store (and RAG when enabled) the way the server does.
func migrateDeps(ctx context.Context) (migrate.Deps, func(), error) {
	if err := config.EnsureHome(); err != nil {
		return migrate.Deps{}, nil, err
	}
	cfg, err := config.Reload()
	if err != nil {
		return migrate.Deps{}, nil, err
	}
	db, err := store.Open(ctx, cfg.Database.Driver, cfg.Database.DSN, cfg.Database.MaxConns, cfg.Database.Busy, cfg.Database.WAL)
	if err != nil {
		return migrate.Deps{}, nil, fmt.Errorf("opening database: %w", err)
	}
	deps := migrate.Deps{Store: db}
	if p, err := rag.New(cfg, db); err == nil && p != nil {
		deps.RAG = p
	}
	return deps, func() { _ = db.Close() }, nil
}

func migrateUndo(ctx context.Context, name string, out io.Writer) error {
	// Accept a path to the backup dir too, as long as it is in the backups dir.
	if strings.ContainsAny(name, `/\`) {
		abs, _ := filepath.Abs(name)
		if filepath.Dir(abs) != filepath.Clean(migrate.BackupsDir()) {
			return fmt.Errorf("%s is not in %s", name, migrate.BackupsDir())
		}
		name = filepath.Base(abs)
	}
	if _, err := migrate.ResolveBackup(name); err != nil {
		return err
	}
	deps, closeFn, err := migrateDeps(ctx)
	if err != nil {
		return err
	}
	defer closeFn()
	if err := migrate.Undo(ctx, name, deps); err != nil {
		return err
	}
	fmt.Fprintf(out, "Undid %s. Restart Antares if it is running so it picks up the restored config.\n", name)
	return nil
}

var migrateCatOrder = []migrate.Category{
	migrate.CatProvider, migrate.CatModel, migrate.CatSoul, migrate.CatAgentsMD, migrate.CatUserMD,
	migrate.CatMemory, migrate.CatKnowledge, migrate.CatSkill, migrate.CatMCP, migrate.CatCron,
	migrate.CatChannel, migrate.CatRole,
}

// validResolution reports whether r applies to items of cat.
func validResolution(cat migrate.Category, r migrate.Resolution) bool {
	switch r {
	case migrate.ResolveSkip, migrate.ResolveReplace:
		return true
	case migrate.ResolveRename:
		return cat == migrate.CatProvider || cat == migrate.CatSkill || cat == migrate.CatMCP || cat == migrate.CatRole
	case migrate.ResolveAppend:
		return cat == migrate.CatSoul || cat == migrate.CatAgentsMD || cat == migrate.CatUserMD
	}
	return false
}

func migrateRun(ctx context.Context, source string, args []string, out io.Writer, interactive bool) error {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	root := fs.String("root", "", "the agent's home dir")
	profile := fs.String("profile", "", "profile/install to import")
	dryRun := fs.Bool("dry-run", false, "print the plan only")
	only := fs.String("only", "", "comma-separated categories")
	yes := fs.Bool("yes", false, "apply without asking")
	conflict := fs.String("conflict", "skip", "skip|replace|rename|append")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("%v\n%s", err, migrateUsage)
	}
	res := migrate.Resolution(*conflict)
	switch res {
	case migrate.ResolveSkip, migrate.ResolveReplace, migrate.ResolveRename, migrate.ResolveAppend:
	default:
		return fmt.Errorf("--conflict must be skip, replace, rename or append")
	}
	keep := map[migrate.Category]bool{}
	for _, c := range strings.Split(*only, ",") {
		if c = strings.TrimSpace(c); c != "" {
			keep[migrate.Category(c)] = true
		}
	}
	if _, ok := migrate.Lookup(source); !ok {
		var ids []string
		for _, s := range migrate.Sources() {
			ids = append(ids, s.ID())
		}
		return fmt.Errorf("unknown source %q (known: %s)", source, strings.Join(ids, ", "))
	}

	if err := config.EnsureHome(); err != nil {
		return err
	}
	cfg, err := config.Reload()
	if err != nil {
		return err
	}
	plan, err := migrate.BuildPlan(ctx, source, *root, *profile, migrate.NewEnv(cfg))
	if err != nil {
		return err
	}
	if len(keep) > 0 {
		kept := plan.Items[:0]
		for _, it := range plan.Items {
			if keep[it.Category] {
				kept = append(kept, it)
			}
		}
		plan.Items = kept
	}
	printPlan(out, plan)
	if *dryRun {
		fmt.Fprintln(out, "\nDry run: nothing was changed.")
		return nil
	}
	if !*yes && !interactive {
		return errors.New("not a terminal: pass --yes to apply (conflicts follow --conflict), or --dry-run")
	}

	var choices []migrate.Choice
	if !*yes {
		if !promptYesNo(fmt.Sprintf("\nImport from %s now?", plan.Detection.Name), true) {
			fmt.Fprintln(out, "Nothing was changed.")
			return nil
		}
	}
	for _, it := range plan.Items {
		switch it.Status {
		case migrate.StatusReady:
			// A ready item the source left unselected (an entry that looks
			// like a credential) is only imported when asked for by name:
			// --yes never takes it, the prompt defaults to no.
			if !it.Selected && (*yes || !promptYesNo(fmt.Sprintf("Also import %q? It looks like it holds a credential.", it.Title), false)) {
				continue
			}
			choices = append(choices, migrate.Choice{ID: it.ID})
		case migrate.StatusConflict:
			r := res
			if !*yes {
				r = askResolution(it)
			}
			if r != migrate.ResolveSkip && validResolution(it.Category, r) {
				choices = append(choices, migrate.Choice{ID: it.ID, Resolution: r})
			}
		case migrate.StatusNeedsInput:
			if *yes {
				continue
			}
			v := strings.TrimSpace(promptSecret(fmt.Sprintf("%s — %s (%s, Enter to skip): ", it.Title, it.Input, it.Reason)))
			if v != "" {
				choices = append(choices, migrate.Choice{ID: it.ID, Input: v})
			}
		}
	}
	if len(choices) == 0 {
		fmt.Fprintln(out, "Nothing to import.")
		return nil
	}
	deps, closeFn, err := migrateDeps(ctx)
	if err != nil {
		return err
	}
	defer closeFn()
	rep, err := migrate.Apply(ctx, plan, choices, deps)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "\nImported %d, skipped %d, failed %d.\n", len(rep.Applied), len(rep.Skipped), len(rep.Failed))
	for _, f := range rep.Failed {
		fmt.Fprintf(out, "  ✗ %s: %s\n", f.ID, f.Error)
	}
	fmt.Fprintf(out, "Backup: %s\nUndo with: antares migrate undo %s\n", filepath.Join(migrate.BackupsDir(), rep.Backup), rep.Backup)
	if rep.NeedsRestart {
		fmt.Fprintln(out, "Restart Antares if it is running so chat channels and MCP servers pick up the changes.")
	}
	return nil
}

func askResolution(it migrate.Item) migrate.Resolution {
	opts := []migrate.Resolution{migrate.ResolveSkip, migrate.ResolveReplace}
	for _, r := range []migrate.Resolution{migrate.ResolveRename, migrate.ResolveAppend} {
		if validResolution(it.Category, r) {
			opts = append(opts, r)
		}
	}
	names := make([]string, len(opts))
	for i, o := range opts {
		names[i] = string(o)
	}
	for {
		v := strings.ToLower(promptLine(fmt.Sprintf("%s: %s — %s?", it.Title, it.Reason, strings.Join(names, "/")), "skip"))
		for _, o := range opts {
			if v == string(o) || (len(v) == 1 && strings.HasPrefix(string(o), v)) {
				return o
			}
		}
	}
}

func printPlan(out io.Writer, plan migrate.Plan) {
	d := plan.Detection
	head := d.Name + " at " + d.Root
	if d.Profile != "" {
		head += " (profile " + d.Profile + ")"
	}
	if d.Version != "" {
		head += " · v" + d.Version
	}
	fmt.Fprintln(out, head)
	for _, w := range plan.Warnings {
		fmt.Fprintln(out, "  ! "+w)
	}
	by := map[migrate.Category][]migrate.Item{}
	for _, it := range plan.Items {
		by[it.Category] = append(by[it.Category], it)
	}
	if len(plan.Items) == 0 {
		fmt.Fprintln(out, "\nNothing to import.")
		return
	}
	for _, cat := range migrateCatOrder {
		items := by[cat]
		if len(items) == 0 {
			continue
		}
		fmt.Fprintf(out, "\n%s (%d)\n", cat, len(items))
		for _, it := range items {
			mark := map[migrate.Status]string{
				migrate.StatusReady: "+", migrate.StatusConflict: "!", migrate.StatusNeedsInput: "?", migrate.StatusUnsupported: "-",
			}[it.Status]
			if it.Status == migrate.StatusReady && !it.Selected {
				mark = "○"
			}
			line := fmt.Sprintf("  %s %s", mark, it.Title)
			if it.Detail != "" {
				line += "  · " + it.Detail
			}
			fmt.Fprintln(out, line)
			if it.Reason != "" && (it.Status != migrate.StatusReady || !it.Selected) {
				fmt.Fprintf(out, "      %s\n", it.Reason)
			}
		}
	}
	fmt.Fprintln(out, "\n  + ready   ○ off by default   ! conflict   ? needs a value   - not supported")
}
