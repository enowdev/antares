package tools

import (
	"context"
	"crypto/md5"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// usernameCandidates derives plausible handles from an email local part (or any
// raw handle): the part before any +tag, and variants with separators/trailing
// digits stripped. Deduplicated, most-specific first.
func usernameCandidates(local string) []string {
	local = strings.ToLower(strings.TrimSpace(local))
	if i := strings.IndexByte(local, '+'); i > 0 { // drop plus-tags: user+tag → user
		local = local[:i]
	}
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		s = strings.Trim(s, ".-_")
		if len(s) >= 2 && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	add(local)
	add(strings.NewReplacer(".", "", "-", "", "_", "").Replace(local)) // collapse separators
	add(regexp.MustCompile(`\d+$`).ReplaceAllString(local, ""))        // strip trailing digits
	return out
}

// Service-backed OSINT lookups — all keyless. They use free, public endpoints
// (no registration or API key), so every OSINT tool works out of the box.

func osintJSON(ctx context.Context, method, url string, headers map[string]string, out any) (int, error) {
	tctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(tctx, method, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "antares-osint/1.0")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := webClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if out != nil && len(body) > 0 {
		_ = json.Unmarshal(body, out)
	}
	return resp.StatusCode, nil
}

// ---- osint_github (keyless) -------------------------------------------------

type osintGithubTool struct{}

func (osintGithubTool) Name() string { return "osint_github" }
func (osintGithubTool) Description() string {
	return "Profile a GitHub user from public data: name, bio, company, location, blog, public repo/follower " +
		"counts, and account age. Keyless."
}
func (osintGithubTool) Schema() map[string]any {
	return schema(map[string]any{"username": prop("string", "The GitHub login to profile.")}, "username")
}
func (osintGithubTool) RequiresApproval() bool { return false }

func (osintGithubTool) Execute(ctx context.Context, in Input) Result {
	var args struct {
		Username string `json:"username"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	user := strings.TrimSpace(args.Username)
	if user == "" {
		return Errorf("username is required")
	}
	var u struct {
		Login, Name, Company, Blog, Location, Email, Bio, CreatedAt string
		PublicRepos, Followers, Following                           int
	}
	status, err := osintJSON(ctx, "GET", "https://api.github.com/users/"+user, nil, &u)
	if err != nil {
		return Errorf("github lookup failed: %v", err)
	}
	if status == 404 {
		return Text(fmt.Sprintf("No GitHub user %q.", user))
	}
	if status != 200 {
		return Errorf("github returned HTTP %d", status)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "GitHub: %s\n\n", u.Login)
	writeIf(&b, "Name", u.Name)
	writeIf(&b, "Bio", u.Bio)
	writeIf(&b, "Company", u.Company)
	writeIf(&b, "Location", u.Location)
	writeIf(&b, "Blog", u.Blog)
	writeIf(&b, "Email", u.Email)
	fmt.Fprintf(&b, "Repos: %d | Followers: %d | Following: %d\n", u.PublicRepos, u.Followers, u.Following)
	writeIf(&b, "Joined", u.CreatedAt)
	fmt.Fprintf(&b, "Profile: https://github.com/%s\n", u.Login)
	return Text(b.String())
}

func writeIf(b *strings.Builder, label, val string) {
	if strings.TrimSpace(val) != "" {
		fmt.Fprintf(b, "%s: %s\n", label, val)
	}
}

// ---- osint_breach (keyless, XposedOrNot) ------------------------------------

// xposedBreaches queries the keyless XposedOrNot API for breaches of an email.
func xposedBreaches(ctx context.Context, email string) (names []string, found bool, err error) {
	var d struct {
		Breaches [][]string `json:"breaches"`
		Error    string     `json:"Error"`
	}
	status, e := osintJSON(ctx, "GET", "https://api.xposedornot.com/v1/check-email/"+email, nil, &d)
	if e != nil {
		return nil, false, e
	}
	if status == 404 || d.Error != "" {
		return nil, false, nil
	}
	if len(d.Breaches) > 0 {
		return d.Breaches[0], len(d.Breaches[0]) > 0, nil
	}
	return nil, false, nil
}

type osintBreachTool struct{}

func (osintBreachTool) Name() string { return "osint_breach" }
func (osintBreachTool) Description() string {
	return "Check whether an email appears in known public data breaches. Keyless (via XposedOrNot). For " +
		"authorized investigations."
}
func (osintBreachTool) Schema() map[string]any {
	return schema(map[string]any{"email": prop("string", "The email address to check.")}, "email")
}
func (osintBreachTool) RequiresApproval() bool { return false }

func (osintBreachTool) Execute(ctx context.Context, in Input) Result {
	var args struct {
		Email string `json:"email"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	email := strings.TrimSpace(strings.ToLower(args.Email))
	if !strings.Contains(email, "@") {
		return Errorf("%q is not a valid email", email)
	}
	names, found, err := xposedBreaches(ctx, email)
	if err != nil {
		return Errorf("breach lookup failed: %v", err)
	}
	if !found {
		return Text(fmt.Sprintf("%s: no breaches found.", email))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s appears in %d breach(es):\n\n", email, len(names))
	for _, n := range names {
		fmt.Fprintf(&b, "- %s\n", n)
	}
	return Text(b.String())
}

// ---- osint_email (keyless) --------------------------------------------------

type osintEmailTool struct{}

func (osintEmailTool) Name() string { return "osint_email" }
func (osintEmailTool) Description() string {
	return "Investigate an email address: Gravatar profile presence and known data-breach exposure. Keyless. " +
		"For authorized investigations."
}
func (osintEmailTool) Schema() map[string]any {
	return schema(map[string]any{"email": prop("string", "The email address to investigate.")}, "email")
}
func (osintEmailTool) RequiresApproval() bool { return false }

func (osintEmailTool) Execute(ctx context.Context, in Input) Result {
	var args struct {
		Email string `json:"email"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	email := strings.TrimSpace(strings.ToLower(args.Email))
	local, domain, ok := strings.Cut(email, "@")
	if !ok {
		return Errorf("%q is not a valid email", email)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Email intelligence for %s\n\n", email)

	// Gravatar (keyless): an existing hash returns a profile.
	sum := md5.Sum([]byte(email))
	hash := hex.EncodeToString(sum[:])
	var grav struct {
		Entry []struct {
			DisplayName string `json:"displayName"`
			AboutMe     string `json:"aboutMe"`
			ProfileUrl  string `json:"profileUrl"`
		} `json:"entry"`
	}
	if status, _ := osintJSON(ctx, "GET", "https://www.gravatar.com/"+hash+".json", nil, &grav); status == 200 && len(grav.Entry) > 0 {
		e := grav.Entry[0]
		b.WriteString("Gravatar: found\n")
		writeIf(&b, "  Name", e.DisplayName)
		writeIf(&b, "  About", e.AboutMe)
		writeIf(&b, "  Profile", e.ProfileUrl)
	} else {
		b.WriteString("Gravatar: none\n")
	}

	// Breaches (keyless).
	names, found, err := xposedBreaches(ctx, email)
	switch {
	case err != nil:
		b.WriteString("Breaches: lookup failed\n")
	case !found:
		b.WriteString("Breaches: none found\n")
	default:
		fmt.Fprintf(&b, "Breaches: %d found:\n", len(names))
		for _, n := range names {
			fmt.Fprintf(&b, "  - %s\n", n)
		}
	}

	// Pivot leads: the local part is the strongest handle candidate. Derive a
	// few normalised variants and hand them to the agent to cross-search with
	// osint_username / osint_pivot — this is what turns an email into linked
	// accounts instead of a dead end.
	cands := usernameCandidates(local)
	fmt.Fprintf(&b, "\nUsername candidates (from the local part) — cross-search these:\n")
	for _, c := range cands {
		fmt.Fprintf(&b, "  - %s\n", c)
	}
	b.WriteString("\nPivot leads:\n")
	fmt.Fprintf(&b, "  - GitHub commits by this email: https://github.com/search?q=%s&type=commits\n", url.QueryEscape(email))
	fmt.Fprintf(&b, "  - Google dork: \"%s\"  https://www.google.com/search?q=%s\n", email, url.QueryEscape("\""+email+"\""))
	if domain != "gmail.com" && domain != "outlook.com" && domain != "yahoo.com" && domain != "hotmail.com" && domain != "proton.me" && domain != "icloud.com" {
		fmt.Fprintf(&b, "  - Custom domain %q — likely personal/company; run osint_domain on it.\n", domain)
	}
	b.WriteString("\nNext: run osint_username on the top candidate, and osint_pivot on any profile you find to extract further emails/handles.\n")

	return Result{Content: b.String(), Meta: map[string]any{
		"email": email, "local": local, "domain": domain,
		"username_candidates": cands, "breaches": len(names),
	}}
}

// ---- osint_shodan (keyless, Shodan InternetDB) ------------------------------

type osintShodanTool struct{}

func (osintShodanTool) Name() string { return "osint_shodan" }
func (osintShodanTool) Description() string {
	return "Look up an IP's exposed surface: open ports, hostnames, technologies (CPEs), and known CVEs. " +
		"Keyless (via Shodan's free InternetDB)."
}
func (osintShodanTool) Schema() map[string]any {
	return schema(map[string]any{"ip": prop("string", "The IP address to look up.")}, "ip")
}
func (osintShodanTool) RequiresApproval() bool { return false }

func (osintShodanTool) Execute(ctx context.Context, in Input) Result {
	var args struct {
		IP string `json:"ip"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	ip := strings.TrimSpace(args.IP)
	if ip == "" {
		return Errorf("ip is required")
	}
	var d struct {
		IP        string   `json:"ip"`
		Ports     []int    `json:"ports"`
		Hostnames []string `json:"hostnames"`
		Cpes      []string `json:"cpes"`
		Vulns     []string `json:"vulns"`
		Tags      []string `json:"tags"`
	}
	status, err := osintJSON(ctx, "GET", "https://internetdb.shodan.io/"+ip, nil, &d)
	if err != nil {
		return Errorf("lookup failed: %v", err)
	}
	if status == 404 {
		return Text(fmt.Sprintf("No exposed-surface records for %s.", ip))
	}
	if status != 200 {
		return Errorf("InternetDB returned HTTP %d", status)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Exposed surface for %s (Shodan InternetDB)\n\n", ip)
	if len(d.Hostnames) > 0 {
		fmt.Fprintf(&b, "Hostnames: %s\n", strings.Join(d.Hostnames, ", "))
	}
	fmt.Fprintf(&b, "Open ports: %s\n", intsJoin(d.Ports))
	if len(d.Cpes) > 0 {
		fmt.Fprintf(&b, "Technologies: %s\n", strings.Join(d.Cpes, ", "))
	}
	if len(d.Tags) > 0 {
		fmt.Fprintf(&b, "Tags: %s\n", strings.Join(d.Tags, ", "))
	}
	if len(d.Vulns) > 0 {
		fmt.Fprintf(&b, "\n⚠ Known CVEs: %s\n", strings.Join(d.Vulns, ", "))
	} else {
		b.WriteString("\nKnown CVEs: none listed\n")
	}
	return Text(b.String())
}

// ---- osint_reputation (keyless, urlscan.io) ---------------------------------

type osintReputationTool struct{}

func (osintReputationTool) Name() string { return "osint_reputation" }
func (osintReputationTool) Description() string {
	return "Check a domain or URL's public scan history and threat sightings via urlscan.io: how many scans " +
		"exist, the pages seen, and any results flagged malicious. Keyless."
}
func (osintReputationTool) Schema() map[string]any {
	return schema(map[string]any{
		"target": prop("string", "A domain (e.g. example.com) or URL to check."),
	}, "target")
}
func (osintReputationTool) RequiresApproval() bool { return false }

func (osintReputationTool) Execute(ctx context.Context, in Input) Result {
	var args struct {
		Target string `json:"target"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	target := strings.TrimSpace(args.Target)
	if target == "" {
		return Errorf("target is required")
	}
	q := strings.TrimPrefix(strings.TrimPrefix(target, "https://"), "http://")
	q = strings.SplitN(q, "/", 2)[0]

	var d struct {
		Total   int `json:"total"`
		Results []struct {
			Task struct {
				URL  string `json:"url"`
				Time string `json:"time"`
			} `json:"task"`
			Verdicts struct {
				Overall struct {
					Malicious bool `json:"malicious"`
					Score     int  `json:"score"`
				} `json:"overall"`
			} `json:"verdicts"`
		} `json:"results"`
	}
	status, err := osintJSON(ctx, "GET", "https://urlscan.io/api/v1/search/?q=domain:"+q+"&size=10", nil, &d)
	if err != nil {
		return Errorf("reputation lookup failed: %v", err)
	}
	if status != 200 {
		return Errorf("urlscan returned HTTP %d", status)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Reputation for %s (urlscan.io)\n\n", q)
	fmt.Fprintf(&b, "Public scans on record: %d\n", d.Total)
	malicious := 0
	for _, r := range d.Results {
		if r.Verdicts.Overall.Malicious {
			malicious++
		}
	}
	if malicious > 0 {
		fmt.Fprintf(&b, "⚠ %d recent scan(s) flagged MALICIOUS\n", malicious)
	}
	if len(d.Results) > 0 {
		b.WriteString("\nRecent scans:\n")
		for i, r := range d.Results {
			if i >= 8 {
				break
			}
			flag := ""
			if r.Verdicts.Overall.Malicious {
				flag = " [malicious]"
			}
			fmt.Fprintf(&b, "- %s (%s)%s\n", r.Task.URL, r.Task.Time, flag)
		}
	} else {
		b.WriteString("\nNo public scans found for this target.\n")
	}
	return Text(b.String())
}

// ---- osint_crypto (keyless) -------------------------------------------------

type osintCryptoTool struct{}

func (osintCryptoTool) Name() string { return "osint_crypto" }
func (osintCryptoTool) Description() string {
	return "Look up a Bitcoin address's on-chain activity: balance, total received/sent, and transaction " +
		"count. Keyless (public blockchain data)."
}
func (osintCryptoTool) Schema() map[string]any {
	return schema(map[string]any{"address": prop("string", "The Bitcoin address to inspect.")}, "address")
}
func (osintCryptoTool) RequiresApproval() bool { return false }

func (osintCryptoTool) Execute(ctx context.Context, in Input) Result {
	var args struct {
		Address string `json:"address"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	addr := strings.TrimSpace(args.Address)
	if addr == "" {
		return Errorf("address is required")
	}
	var d struct {
		NTx           int   `json:"n_tx"`
		TotalReceived int64 `json:"total_received"`
		TotalSent     int64 `json:"total_sent"`
		FinalBalance  int64 `json:"final_balance"`
	}
	status, err := osintJSON(ctx, "GET", "https://blockchain.info/rawaddr/"+addr+"?limit=1", nil, &d)
	if err != nil {
		return Errorf("crypto lookup failed: %v", err)
	}
	if status != 200 {
		return Errorf("blockchain lookup returned HTTP %d (is the address valid?)", status)
	}
	btc := func(sat int64) float64 { return float64(sat) / 1e8 }
	var b strings.Builder
	fmt.Fprintf(&b, "Bitcoin address %s\n\n", addr)
	fmt.Fprintf(&b, "Balance: %.8f BTC\n", btc(d.FinalBalance))
	fmt.Fprintf(&b, "Total received: %.8f BTC\n", btc(d.TotalReceived))
	fmt.Fprintf(&b, "Total sent: %.8f BTC\n", btc(d.TotalSent))
	fmt.Fprintf(&b, "Transactions: %d\n", d.NTx)
	return Text(b.String())
}

// ---- helpers ----------------------------------------------------------------

func intsJoin(nums []int) string {
	parts := make([]string, len(nums))
	for i, n := range nums {
		parts[i] = fmt.Sprintf("%d", n)
	}
	return strings.Join(parts, ", ")
}

// ---- osint_google (optional, cookie-gated) ----------------------------------

// osintGoogleTool resolves an email to its public Google account profile — the
// name, profile photo, and (when present) Google-surfaced activity — the way
// GHunt does. It needs a logged-in Google session Cookie header in config
// (osint.google_cookie); without one it explains how to enable it rather than
// failing silently. Uses only the account owner's own session and reads public
// profile data; ToS-sensitive, so the operator opts in by supplying the cookie.
type osintGoogleTool struct{}

func (osintGoogleTool) Name() string { return "osint_google" }
func (osintGoogleTool) Description() string {
	return "Resolve an email to its public Google account profile (display name, profile photo, " +
		"last-updated hints), GHunt-style. Requires a Google session cookie configured under " +
		"osint.google_cookie; without it, the tool explains how to enable it. For authorized investigations."
}
func (osintGoogleTool) Schema() map[string]any {
	return schema(map[string]any{
		"email": prop("string", "The Gmail/Google address to resolve to a public profile."),
	}, "email")
}
func (osintGoogleTool) RequiresApproval() bool { return false }

func (osintGoogleTool) Execute(ctx context.Context, in Input) Result {
	var args struct {
		Email string `json:"email"`
	}
	if err := in.Bind(&args); err != nil {
		return Errorf("%v", err)
	}
	email := strings.TrimSpace(strings.ToLower(args.Email))
	if !strings.Contains(email, "@") {
		return Errorf("%q is not a valid email", email)
	}

	raw := ""
	if in.Deps != nil && in.Deps.Config != nil {
		raw = strings.TrimSpace(in.Deps.Config.OSINT.GoogleCookie)
	}
	if raw == "" {
		return Text("Google profile lookup is not enabled.\n\n" +
			"Easiest way to get the cookie:\n" +
			"  1. Install the Cookie-Editor extension: https://chromewebstore.google.com/detail/cookie-editor/hlkenndednhfkekhgcdicdfddnkalmdm\n" +
			"  2. Sign in to any Google account, open google.com, click the Cookie-Editor icon.\n" +
			"  3. Click Export → Export as JSON (copies the cookie array to your clipboard).\n" +
			"  4. Paste that JSON into Settings → OSINT → Google Cookie (stored redacted).\n\n" +
			"A raw \"name=value; …\" Cookie header works too. The JSON must include SAPISID (the key one), " +
			"plus SID/HSID/SSID/APISID.\n" +
			"Note: it expires within hours–days — re-export if lookups start failing. Optional and ToS-sensitive.\n" +
			"Meanwhile, osint_email already gives username candidates and a GitHub-commit pivot for " + email + ".")
	}
	cookie, sapisid := googleCookieHeader(raw)
	if cookie == "" {
		return Errorf("could not parse the Google cookie — paste the Cookie-Editor JSON export, or a raw \"name=value; …\" header")
	}
	if sapisid == "" {
		return Text("The cookie is missing SAPISID, which Google's profile API needs. Re-export the full cookie set with Cookie-Editor.")
	}

	// Google's People API People:lookup by email. It authenticates with a
	// SAPISIDHASH built from the SAPISID cookie, a timestamp, and the origin —
	// the same scheme browser XHRs use. Endpoints/gating change often; treat
	// failure as "unknown".
	const origin = "https://myaccount.google.com"
	authUser := 0
	if in.Deps != nil && in.Deps.Config != nil {
		authUser = in.Deps.Config.OSINT.GoogleAuthUser
	}
	endpoint := fmt.Sprintf("https://people-pa.clients6.google.com/v2/people/lookup?id=%s"+
		"&type=EMAIL&matchType=EXACT&extensionSet.extensionNames=HANGOUTS_ADDITIONAL_DATA&authuser=%d",
		url.QueryEscape(email), authUser)
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return Errorf("%v", err)
	}
	req.Header.Set("Cookie", cookie)
	req.Header.Set("Authorization", sapisidHash(sapisid, origin))
	req.Header.Set("Origin", origin)
	req.Header.Set("Referer", origin+"/")
	req.Header.Set("X-Origin", origin)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Accept", "application/json")
	resp, err := webClient.Do(req)
	if err != nil {
		return Errorf("Google lookup failed to connect: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return Text("Google rejected the session cookie (HTTP " + fmt.Sprint(resp.StatusCode) +
			"). It may be expired — refresh osint.google_cookie from a current session.")
	}
	if resp.StatusCode != 200 {
		return Errorf("Google returned HTTP %d", resp.StatusCode)
	}

	// The response shape is deeply nested and version-specific; rather than pin
	// a brittle struct, surface the raw JSON for the model to read and pull the
	// display name / photo / profile links from. Trimmed to keep it manageable.
	var b strings.Builder
	fmt.Fprintf(&b, "Google profile lookup for %s (HTTP 200)\n\n", email)
	trimmed := string(body)
	if len(trimmed) > 6000 {
		trimmed = trimmed[:6000] + "\n…(truncated)"
	}
	if strings.TrimSpace(trimmed) == "" || strings.Contains(trimmed, `"people":{}`) || strings.Contains(trimmed, `"matches":[]`) {
		b.WriteString("No public Google profile resolved for this address (it may not be a Google account, " +
			"or the profile is private).\n")
	} else {
		b.WriteString("Raw People API response (extract the display name, photo URL, and any profile/plus links):\n\n")
		b.WriteString(trimmed)
		b.WriteString("\n")
	}
	return Text(b.String())
}

// googleCookieHeader turns the configured Google cookie — either a Cookie-Editor
// JSON export ([{"name","value",...}]) or a raw "name=value; …" header — into a
// Cookie header string, and returns the SAPISID value separately (the People
// API authenticates with a SAPISIDHASH derived from it).
func googleCookieHeader(raw string) (header, sapisid string) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "[") {
		var entries []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		}
		if json.Unmarshal([]byte(raw), &entries) != nil {
			return "", ""
		}
		parts := make([]string, 0, len(entries))
		for _, e := range entries {
			if e.Name == "" {
				continue
			}
			parts = append(parts, e.Name+"="+e.Value)
			if e.Name == "SAPISID" || (sapisid == "" && e.Name == "__Secure-3PAPISID") {
				sapisid = e.Value
			}
		}
		return strings.Join(parts, "; "), sapisid
	}
	// Raw header: also scan it for SAPISID.
	for _, kv := range strings.Split(raw, ";") {
		kv = strings.TrimSpace(kv)
		if name, val, ok := strings.Cut(kv, "="); ok {
			if name == "SAPISID" || (sapisid == "" && name == "__Secure-3PAPISID") {
				sapisid = val
			}
		}
	}
	return raw, sapisid
}

// sapisidHash builds the Authorization value Google's *-pa APIs expect from a
// browser session: "SAPISIDHASH <ts>_<sha1(ts + ' ' + sapisid + ' ' + origin)>".
func sapisidHash(sapisid, origin string) string {
	ts := time.Now().Unix()
	sum := sha1.Sum([]byte(fmt.Sprintf("%d %s %s", ts, sapisid, origin)))
	return fmt.Sprintf("SAPISIDHASH %d_%s", ts, hex.EncodeToString(sum[:]))
}

// GoogleAccount is a logged-in Google account resolved from a session cookie.
// AuthUser is the /u/<N>/ index that selects it in a multi-account session.
type GoogleAccount struct {
	AuthUser int    `json:"authuser"`
	Email    string `json:"email"`
	Name     string `json:"name"`
}

// googleAccountAt loads myaccount.google.com/u/<n>/ and returns the email shown
// there (the most-referenced address in the bootstrap data). Empty when the
// slot has no distinct account.
func googleAccountAt(ctx context.Context, cookie string, n int) (email string) {
	tctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(tctx, "GET",
		fmt.Sprintf("https://myaccount.google.com/u/%d/", n), nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Cookie", cookie)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0 Safari/537.36")
	resp, err := webClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		if resp != nil {
			resp.Body.Close()
		}
		return ""
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 3<<20))
	// The active account's email dominates the page; take the most frequent.
	counts := map[string]int{}
	for _, m := range reGoogleEmail.FindAllStringSubmatch(string(body), -1) {
		counts[m[1]]++
	}
	best, bestN := "", 0
	for e, c := range counts {
		if c > bestN {
			best, bestN = e, c
		}
	}
	return best
}

// VerifyGoogleCookie checks whether a stored Google cookie is a live session
// and returns the account(s) it belongs to. It accepts the same input the
// osint_google tool does (Cookie-Editor JSON or a raw header). The
// accounts.google.com/ListAccounts endpoint answers to the cookie alone, so it
// doubles as a "is this cookie still valid?" probe.
func VerifyGoogleCookie(ctx context.Context, raw string) (accounts []GoogleAccount, err error) {
	cookie, sapisid := googleCookieHeader(raw)
	if strings.TrimSpace(cookie) == "" {
		return nil, fmt.Errorf("no cookie configured")
	}
	if sapisid == "" {
		return nil, fmt.Errorf("cookie is missing SAPISID — re-export the full cookie set with Cookie-Editor")
	}
	_ = sapisid

	// A session can hold several accounts, selected by the /u/<N>/ path. Walk the
	// slots and collect distinct emails; Google wraps an out-of-range index back
	// to the default account, so stop once an email repeats one already seen or a
	// slot yields nothing. This lists every account the cookie is signed into so
	// the user can pick the right one.
	seen := map[string]bool{}
	for n := 0; n < 8; n++ {
		email := googleAccountAt(ctx, cookie, n)
		if email == "" {
			// The first slot must resolve; a blank slot 0 means the cookie is dead.
			if n == 0 {
				continue
			}
			break
		}
		if seen[email] {
			break // wrapped back to an earlier account — no more distinct slots
		}
		seen[email] = true
		accounts = append(accounts, GoogleAccount{AuthUser: n, Email: email})
	}
	if len(accounts) == 0 {
		return nil, fmt.Errorf("cookie did not authenticate — Google served a signed-out page; re-export the cookie")
	}
	return accounts, nil
}

var (
	// The account email is embedded in myaccount.google.com bootstrap JSON.
	reGoogleEmail = regexp.MustCompile(`"([a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,})"`)
	// A display name often rides alongside as ["Full Name", ...].
	reGoogleName = regexp.MustCompile(`"name":\s*"([^"]{1,80})"`)
)

func firstMatch(re *regexp.Regexp, s string) string {
	if m := re.FindStringSubmatch(s); len(m) > 1 {
		return m[1]
	}
	return ""
}
