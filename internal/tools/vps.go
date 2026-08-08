package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/enowdev/antares/internal/store"
	"github.com/enowdev/antares/internal/vps"
)

// ---- shared host resolution -------------------------------------------------

// resolveVPSHost picks a saved host by id or case-insensitive label.
func resolveVPSHost(ctx context.Context, in Input, ref string) (*store.VPSHost, error) {
	if in.Deps == nil || in.Deps.Store == nil {
		return nil, fmt.Errorf("no VPS store available in this runtime")
	}
	hosts, err := in.Deps.Store.ListVPSHosts(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not read saved servers: %v", err)
	}
	if len(hosts) == 0 {
		return nil, fmt.Errorf("no servers are saved. Add one on the dashboard's VPS page (host, port, user, and a password or SSH key), then reference it by id or label")
	}
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, fmt.Errorf("vps is required — call vps_run with no command to list saved servers")
	}
	for i := range hosts {
		h := &hosts[i]
		if h.ID == ref || strings.EqualFold(h.Label, ref) {
			return h, nil
		}
	}
	return nil, fmt.Errorf("no saved server matches %q — call vps_run with no command to list them", ref)
}

func targetFromHost(h *store.VPSHost) vps.Target {
	return vps.Target{
		Host: h.Host, Port: h.Port, Username: h.Username, AuthMethod: h.AuthMethod,
		Password: h.Password, PrivateKey: h.PrivateKey, Passphrase: h.Passphrase,
		KnownHostKey: h.HostKey,
	}
}

func pinIfNeeded(ctx context.Context, in Input, h *store.VPSHost, seen string, err error) {
	if h.HostKey != "" || seen == "" || errors.Is(err, vps.ErrHostKeyChanged) {
		return
	}
	if in.Deps != nil && in.Deps.Store != nil {
		_ = in.Deps.Store.SetVPSHostKey(ctx, h.ID, seen)
	}
}

func formatVPSList(hosts []store.VPSHost) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Saved servers (%d) — pass id or label as `vps`:\n\n", len(hosts))
	for _, h := range hosts {
		fmt.Fprintf(&b, "  - id=%s  label=%q  %s@%s:%d\n", h.ID, h.Label, h.Username, h.Host, h.Port)
	}
	return b.String()
}

// clampVPSTimeout returns a sane duration for a VPS tool call. Default 120s
// (systemctl restart/stop often exceeds 60s). Hard cap 900s matches the schema.
func clampVPSTimeout(seconds int) time.Duration {
	if seconds <= 0 {
		seconds = 120
	}
	if seconds > 900 {
		seconds = 900
	}
	return time.Duration(seconds) * time.Second
}

// ---- vps_run ----------------------------------------------------------------

// vpsRunTool runs a shell command on one of the user's saved VPS hosts over SSH
// and returns its output. It is the muscle behind the "VPS manager" skill:
// inspect services, read logs, restart things, deploy, update packages — the
// agent decides the command, this executes it on the chosen host.
type vpsRunTool struct{}

func (vpsRunTool) Name() string { return "vps_run" }
func (vpsRunTool) Description() string {
	return "Run a shell command on one of the user's saved VPS servers over SSH and return its output. " +
		"Call with no command to list the available servers (id + label + host). Pass `vps` as a server's id " +
		"or label. Use it to inspect and manage a server — systemctl, journalctl, docker, df, apt/yum, deploys. " +
		"For file copy use vps_upload / vps_download (SFTP). Default timeout is 120s (raise timeout_seconds for " +
		"systemctl restart, apt upgrade, long deploys). Commands run as the configured SSH user."
}
func (vpsRunTool) Schema() map[string]any {
	return schema(map[string]any{
		"vps":             prop("string", "Which server: its id or label (call with no command to list them)."),
		"command":         prop("string", "The shell command to run. Omit to just list the saved servers."),
		"timeout_seconds": propDefault("integer", "How long to allow the command to run (default 120, max 900). systemctl restart/stop often needs >60s.", 120),
	})
}

// RequiresApproval: running arbitrary commands on a live server is a mutating,
// potentially destructive action — gate it behind approval like the terminal.
func (vpsRunTool) RequiresApproval() bool { return true }

func (vpsRunTool) Execute(ctx context.Context, in Input) Result {
	var args struct {
		VPS     string `json:"vps"`
		Command string `json:"command"`
		Timeout int    `json:"timeout_seconds"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	if in.Deps == nil || in.Deps.Store == nil {
		return Errorf("no VPS store available in this runtime")
	}

	hosts, err := in.Deps.Store.ListVPSHosts(ctx)
	if err != nil {
		return Errorf("could not read saved servers: %v", err)
	}
	if len(hosts) == 0 {
		return Text("No servers are saved. Add one on the dashboard's VPS page (host, port, user, and a password or SSH key), then reference it by id or label.")
	}

	// No command → list the servers so the agent can pick one.
	ref := strings.TrimSpace(args.VPS)
	if strings.TrimSpace(args.Command) == "" || ref == "" {
		return Text(formatVPSList(hosts))
	}

	var host *store.VPSHost
	for i := range hosts {
		h := &hosts[i]
		if h.ID == ref || strings.EqualFold(h.Label, ref) {
			host = h
			break
		}
	}
	if host == nil {
		return Errorf("no saved server matches %q — call vps_run with no command to list them", ref)
	}

	timeout := clampVPSTimeout(args.Timeout)
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	label := host.Label
	if label == "" {
		label = host.Host
	}
	in.Emit(Progress{Tool: "vps_run", Message: fmt.Sprintf("running on %s…", label)})
	out, seen, err := vps.Run(runCtx, targetFromHost(host), args.Command)
	pinIfNeeded(ctx, in, host, seen, err)
	out = strings.TrimRight(out, "\n")
	if err != nil {
		if errors.Is(err, vps.ErrTimeout) || errors.Is(err, context.DeadlineExceeded) {
			msg := fmt.Sprintf("Command timed out after %s on %s. "+
				"Increase timeout_seconds (max 900). systemctl restart/stop and package upgrades often need 120–300s. "+
				"Prefer non-interactive flags (e.g. systemctl status NAME --no-pager).",
				timeout, label)
			if out != "" {
				return Result{Content: msg + "\n\nPartial output:\n" + out, IsError: true}
			}
			return Errorf("%s", msg)
		}
		msg := err.Error()
		if out != "" {
			return Result{Content: fmt.Sprintf("Command failed on %s: %s\n\n%s", label, msg, out), IsError: true}
		}
		return Errorf("command failed on %s: %s", label, msg)
	}
	if out == "" {
		out = "(no output)"
	}
	return Text(fmt.Sprintf("On %s:\n\n%s", label, out))
}

// ---- vps_upload -------------------------------------------------------------

type vpsUploadTool struct{}

func (vpsUploadTool) Name() string { return "vps_upload" }
func (vpsUploadTool) Description() string {
	return "Upload a local workspace file to a dashboard-saved VPS over SFTP (preferred over rsync/scp/terminal). " +
		"`local_path` is relative to the workspace (or absolute inside write roots); " +
		"`remote_path` is the destination on the server. Creates remote parent dirs. " +
		"Max 256 MiB, one file per call. Call vps_run with no command first if you need the server id/label. " +
		"Do not use terminal rsync/scp for saved VPS hosts when this tool is available."
}
func (vpsUploadTool) Schema() map[string]any {
	return schema(map[string]any{
		"vps":             prop("string", "Server id or label."),
		"local_path":      prop("string", "Local file path (workspace-relative or absolute in write roots)."),
		"remote_path":     prop("string", "Destination path on the VPS (absolute or relative to the SSH user's home)."),
		"timeout_seconds": propDefault("integer", "Transfer timeout in seconds.", 120),
	}, "vps", "local_path", "remote_path")
}
func (vpsUploadTool) RequiresApproval() bool { return true }

func (vpsUploadTool) Execute(ctx context.Context, in Input) Result {
	var args struct {
		VPS        string `json:"vps"`
		LocalPath  string `json:"local_path"`
		RemotePath string `json:"remote_path"`
		Timeout    int    `json:"timeout_seconds"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	host, err := resolveVPSHost(ctx, in, args.VPS)
	if err != nil {
		return Errorf("%v", err)
	}
	local, err := resolveRead(in, args.LocalPath)
	if err != nil {
		return Errorf("%v", err)
	}
	// Upload reads a local file; ensure it is inside a writable root when in a
	// project session so the agent cannot scoop arbitrary system files onto the
	// VPS without the same boundary write_file would enforce. Ordinary sessions
	// already confine resolveRead to the workspace.
	if len(in.WriteRoots) > 0 {
		if _, err := resolveWrite(in, args.LocalPath); err != nil {
			return Errorf("local_path must be inside the project or Antares workspace: %v", err)
		}
	}

	timeout := clampVPSTimeout(args.Timeout)
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	label := host.Label
	if label == "" {
		label = host.Host
	}
	in.Emit(Progress{Tool: "vps_upload", Message: fmt.Sprintf("uploading to %s…", label)})
	n, seen, err := vps.Upload(runCtx, targetFromHost(host), local, args.RemotePath)
	pinIfNeeded(ctx, in, host, seen, err)
	if err != nil {
		if errors.Is(err, vps.ErrTimeout) || errors.Is(err, context.DeadlineExceeded) {
			return Errorf("upload timed out after %s on %s: %v", timeout, label, err)
		}
		return Errorf("upload to %s failed: %v", label, err)
	}
	return Text(fmt.Sprintf("Uploaded %s → %s@%s:%s (%d bytes)",
		relTo(in.Workspace, local), host.Username, label, args.RemotePath, n))
}

// ---- vps_download -----------------------------------------------------------

type vpsDownloadTool struct{}

func (vpsDownloadTool) Name() string { return "vps_download" }
func (vpsDownloadTool) Description() string {
	return "Download a file from a dashboard-saved VPS over SFTP into the local workspace (preferred over rsync/scp/terminal). " +
		"`remote_path` is on the server; `local_path` is the destination (workspace-relative). " +
		"Creates local parent dirs. Max 256 MiB, one file per call. " +
		"Do not use terminal rsync/scp for saved VPS hosts when this tool is available."
}
func (vpsDownloadTool) Schema() map[string]any {
	return schema(map[string]any{
		"vps":             prop("string", "Server id or label."),
		"remote_path":     prop("string", "Source path on the VPS."),
		"local_path":      prop("string", "Local destination path (must be inside the workspace / write roots)."),
		"timeout_seconds": propDefault("integer", "Transfer timeout in seconds.", 120),
	}, "vps", "remote_path", "local_path")
}
func (vpsDownloadTool) RequiresApproval() bool { return true }

func (vpsDownloadTool) Execute(ctx context.Context, in Input) Result {
	var args struct {
		VPS        string `json:"vps"`
		RemotePath string `json:"remote_path"`
		LocalPath  string `json:"local_path"`
		Timeout    int    `json:"timeout_seconds"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	host, err := resolveVPSHost(ctx, in, args.VPS)
	if err != nil {
		return Errorf("%v", err)
	}
	local, err := resolveWrite(in, args.LocalPath)
	if err != nil {
		return Errorf("%v", err)
	}

	timeout := clampVPSTimeout(args.Timeout)
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	label := host.Label
	if label == "" {
		label = host.Host
	}
	in.Emit(Progress{Tool: "vps_download", Message: fmt.Sprintf("downloading from %s…", label)})
	n, seen, err := vps.Download(runCtx, targetFromHost(host), args.RemotePath, local)
	pinIfNeeded(ctx, in, host, seen, err)
	if err != nil {
		if errors.Is(err, vps.ErrTimeout) || errors.Is(err, context.DeadlineExceeded) {
			return Errorf("download timed out after %s on %s: %v", timeout, label, err)
		}
		return Errorf("download from %s failed: %v", label, err)
	}
	return Text(fmt.Sprintf("Downloaded %s@%s:%s → %s (%d bytes)",
		host.Username, label, args.RemotePath, relTo(in.Workspace, local), n))
}
