package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/store"
)

const deviceUsage = `Usage:
  antares device pair [--name N] [--platform P] [--json]   Create a device token
  antares device list [--json]                             List paired devices
  antares device revoke <id>                               Revoke a device

pair writes straight to the local database, so it works whether or not the
server is running. The token is shown once. Platforms: desktop-macos,
desktop-windows, desktop-linux, cli, other.`

func cmdDevice(args []string) error {
	sub := "list"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "pair":
		return cmdDevicePair(os.Stdout, args)
	case "list", "ls":
		return cmdDeviceList(os.Stdout, args)
	case "revoke", "rm":
		return cmdDeviceRevoke(os.Stdout, args)
	case "help", "--help", "-h":
		fmt.Println(deviceUsage)
		return nil
	default:
		fmt.Fprintln(os.Stderr, deviceUsage)
		return fmt.Errorf("unknown device command: %s", sub)
	}
}

// openDeviceStore opens the local database without the rest of the runtime.
func openDeviceStore(ctx context.Context) (*config.Config, store.Store, error) {
	if err := config.EnsureHome(); err != nil {
		return nil, nil, fmt.Errorf("preparing %s: %w", config.Home(), err)
	}
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, err
	}
	db, err := store.Open(ctx, cfg.Database.Driver, cfg.Database.DSN,
		cfg.Database.MaxConns, cfg.Database.Busy, cfg.Database.WAL)
	if err != nil {
		return nil, nil, fmt.Errorf("opening database: %w", err)
	}
	return cfg, db, nil
}

type devicePairOptions struct {
	Name, Platform string
	JSON           bool
}

func parseDevicePairArgs(args []string) (devicePairOptions, error) {
	var o devicePairOptions
	for i := 0; i < len(args); i++ {
		a := args[i]
		key, val, hasVal := strings.Cut(a, "=")
		takeVal := func() (string, error) {
			if hasVal {
				return val, nil
			}
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s needs a value", key)
			}
			i++
			return args[i], nil
		}
		switch key {
		case "--name", "-n":
			v, err := takeVal()
			if err != nil {
				return o, err
			}
			o.Name = v
		case "--platform", "-p":
			v, err := takeVal()
			if err != nil {
				return o, err
			}
			o.Platform = v
		case "--json":
			o.JSON = true
		default:
			return o, fmt.Errorf("unknown flag: %s", a)
		}
	}
	if strings.TrimSpace(o.Name) == "" {
		host, _ := os.Hostname()
		o.Name = strings.TrimSuffix(host, ".local")
		if strings.TrimSpace(o.Name) == "" {
			o.Name = "This computer"
		}
	}
	if o.Platform == "" {
		o.Platform = "cli"
	}
	return o, nil
}

// localServerURL is the running daemon's URL from its state file, or the
// configured host:port when no daemon is running.
func localServerURL(cfg *config.Config) string {
	if state, err := readDaemonState(); err == nil && state.URL != "" {
		if live, err := validateDaemonState(state); err == nil && live {
			return state.URL
		}
	}
	return daemonURL(cfg)
}

func deviceJSON(d *store.Device) map[string]any {
	iso := func(t *time.Time) any {
		if t == nil {
			return nil
		}
		return t.UTC().Format(time.RFC3339)
	}
	return map[string]any{
		"id": d.ID, "name": d.Name, "platform": d.Platform,
		"created_at":   d.CreatedAt.UTC().Format(time.RFC3339),
		"last_seen_at": iso(d.LastSeenAt),
		"revoked_at":   iso(d.RevokedAt),
	}
}

func cmdDevicePair(w io.Writer, args []string) error {
	o, err := parseDevicePairArgs(args)
	if err != nil {
		return err
	}
	ctx := context.Background()
	cfg, db, err := openDeviceStore(ctx)
	if err != nil {
		return err
	}
	defer db.Close()
	if len([]rune(strings.TrimSpace(o.Name))) > store.DeviceNameMax {
		o.Name = string([]rune(strings.TrimSpace(o.Name))[:store.DeviceNameMax])
	}
	d, token, err := store.PairDevice(ctx, db, o.Name, o.Platform)
	if err != nil {
		return err
	}
	url := localServerURL(cfg)
	if o.JSON {
		enc := json.NewEncoder(w)
		return enc.Encode(map[string]any{
			"device": map[string]any{
				"id": d.ID, "name": d.Name, "platform": d.Platform,
				"created_at": d.CreatedAt.UTC().Format(time.RFC3339),
			},
			"token": token,
			"url":   url,
		})
	}
	fmt.Fprintf(w, "Paired %s (%s, %s)\n", d.Name, d.ID, d.Platform)
	fmt.Fprintf(w, "Token:  %s\n", token)
	fmt.Fprintf(w, "Server: %s\n", url)
	fmt.Fprintln(w, "The token is shown once. Revoke it with: antares device revoke "+d.ID)
	return nil
}

func cmdDeviceList(w io.Writer, args []string) error {
	asJSON := false
	for _, a := range args {
		switch a {
		case "--json":
			asJSON = true
		default:
			return fmt.Errorf("unknown flag: %s", a)
		}
	}
	ctx := context.Background()
	_, db, err := openDeviceStore(ctx)
	if err != nil {
		return err
	}
	defer db.Close()
	devs, err := db.ListDevices(ctx)
	if err != nil {
		return err
	}
	if asJSON {
		out := make([]map[string]any, 0, len(devs))
		for i := range devs {
			out = append(out, deviceJSON(&devs[i]))
		}
		return json.NewEncoder(w).Encode(map[string]any{"devices": out})
	}
	if len(devs) == 0 {
		fmt.Fprintln(w, "No paired devices. Pair one with: antares device pair")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tPLATFORM\tCREATED\tLAST SEEN\tSTATUS")
	for _, d := range devs {
		seen := "never"
		if d.LastSeenAt != nil {
			seen = d.LastSeenAt.Local().Format("2006-01-02 15:04")
		}
		status := "active"
		if d.RevokedAt != nil {
			status = "revoked " + d.RevokedAt.Local().Format("2006-01-02")
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", d.ID, d.Name, d.Platform,
			d.CreatedAt.Local().Format("2006-01-02 15:04"), seen, status)
	}
	return tw.Flush()
}

func cmdDeviceRevoke(w io.Writer, args []string) error {
	if len(args) != 1 || strings.HasPrefix(args[0], "-") {
		return errors.New("usage: antares device revoke <id>")
	}
	ctx := context.Background()
	_, db, err := openDeviceStore(ctx)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := db.RevokeDevice(ctx, args[0]); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("no device %s", args[0])
		}
		return err
	}
	fmt.Fprintf(w, "Revoked %s. A running server stops accepting it within 30 seconds.\n", args[0])
	return nil
}
