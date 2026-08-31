package httpshim

import (
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
)

// runCurl emulates the common subset of curl over the fingerprinted client. Any
// flag or usage it does not recognise sends the whole invocation to the real
// curl, so behaviour never silently changes.
func runCurl(args []string) int {
	var (
		method    string
		url       string
		headers   = map[string]string{}
		body      []byte
		haveBody  bool
		output    string // -o file; "-" or "" means stdout
		useRemote bool   // -O
		include   bool   // -i
		headOnly  bool   // -I
		failFast  bool   // -f
		timeout   time.Duration
	)

	setHeader := func(h string) {
		if k, v, ok := strings.Cut(h, ":"); ok {
			headers[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}

	// next pulls the value for a flag that takes one, either "--flag=value" or
	// the following argument.
	i := 0
	nextVal := func(inline string, hasInline bool) (string, bool) {
		if hasInline {
			return inline, true
		}
		if i+1 >= len(args) {
			return "", false
		}
		i++
		return args[i], true
	}

	for ; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			// Everything after is positional.
			for j := i + 1; j < len(args); j++ {
				if url != "" || !isHTTPURL(args[j]) {
					return fallback("curl", args)
				}
				url = args[j]
			}
			i = len(args)
		case strings.HasPrefix(a, "--"):
			name, inline, hasInline := cutFlag(a)
			switch name {
			case "--request":
				v, ok := nextVal(inline, hasInline)
				if !ok {
					return fallback("curl", args)
				}
				method = strings.ToUpper(v)
			case "--header":
				v, ok := nextVal(inline, hasInline)
				if !ok {
					return fallback("curl", args)
				}
				setHeader(v)
			case "--data", "--data-ascii", "--data-raw", "--data-binary":
				v, ok := nextVal(inline, hasInline)
				if !ok {
					return fallback("curl", args)
				}
				if name != "--data-raw" && strings.HasPrefix(v, "@") {
					data, err := os.ReadFile(v[1:])
					if err != nil {
						fmt.Fprintf(os.Stderr, "curl: cannot read %s: %v\n", v[1:], err)
						return 26
					}
					v = string(data)
				}
				body = append(body, []byte(v)...)
				haveBody = true
			case "--json":
				v, ok := nextVal(inline, hasInline)
				if !ok {
					return fallback("curl", args)
				}
				body = append(body, []byte(v)...)
				haveBody = true
				if _, ok := lookupHeader(headers, "content-type"); !ok {
					headers["Content-Type"] = "application/json"
				}
				if _, ok := lookupHeader(headers, "accept"); !ok {
					headers["Accept"] = "application/json"
				}
			case "--user-agent":
				if v, ok := nextVal(inline, hasInline); ok {
					headers["User-Agent"] = v
				}
			case "--referer":
				if v, ok := nextVal(inline, hasInline); ok {
					headers["Referer"] = v
				}
			case "--cookie":
				if v, ok := nextVal(inline, hasInline); ok {
					headers["Cookie"] = v
				}
			case "--user":
				v, ok := nextVal(inline, hasInline)
				if !ok {
					return fallback("curl", args)
				}
				headers["Authorization"] = "Basic " + base64.StdEncoding.EncodeToString([]byte(v))
			case "--output":
				v, ok := nextVal(inline, hasInline)
				if !ok {
					return fallback("curl", args)
				}
				output = v
			case "--remote-name":
				useRemote = true
			case "--max-time", "--connect-timeout":
				v, ok := nextVal(inline, hasInline)
				if !ok {
					return fallback("curl", args)
				}
				timeout = parseSeconds(v)
			case "--head":
				headOnly = true
			case "--include":
				include = true
			case "--fail", "--fail-with-body":
				failFast = true
			case "--location", "--silent", "--show-error", "--compressed",
				"--verbose", "--no-progress-meter", "--http1.1", "--http2",
				"--http2-prior-knowledge", "--no-buffer", "--fail-early":
				// Known no-ops: httpcloak follows redirects, decompresses, and
				// negotiates the protocol; verbosity/progress do not apply here.
			default:
				// An unrecognised long flag might change semantics — defer to curl.
				return fallback("curl", args)
			}
		case len(a) >= 2 && a[0] == '-' && a != "-":
			// A short flag, possibly bundled (-sSL) or with an attached value.
			consumed, ok := parseShortCurl(a, args, &i, &method, &url, setHeader,
				&body, &haveBody, &output, &useRemote, &include, &headOnly, &failFast, &timeout, headers)
			if !ok || !consumed {
				return fallback("curl", args)
			}
		default:
			// Positional: must be the single http(s) URL.
			if url != "" || !isHTTPURL(a) {
				return fallback("curl", args)
			}
			url = a
		}
	}

	if url == "" {
		return fallback("curl", args)
	}
	if method == "" {
		switch {
		case headOnly:
			method = "HEAD"
		case haveBody:
			method = "POST"
		default:
			method = "GET"
		}
	}

	resp, err := doRequest(method, url, headers, body, timeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "curl: (7) %v\n", err)
		return 7
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	if failFast && resp.StatusCode >= 400 {
		fmt.Fprintf(os.Stderr, "curl: (22) The requested URL returned error: %d\n", resp.StatusCode)
		return 22
	}

	var out strings.Builder
	if include || headOnly {
		out.WriteString(statusLine(resp) + "\n")
		keys := make([]string, 0, len(resp.Headers))
		for k := range resp.Headers {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			out.WriteString(k + ": " + strings.Join(resp.Headers[k], ", ") + "\n")
		}
		out.WriteString("\n")
	}
	if !headOnly {
		out.Write(respBody)
	}

	dest := output
	if useRemote && dest == "" {
		dest = basename(url, "index.html")
	}
	if dest != "" && dest != "-" {
		if err := os.WriteFile(dest, []byte(out.String()), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "curl: cannot write %s: %v\n", dest, err)
			return 23
		}
	} else {
		os.Stdout.WriteString(out.String())
	}
	return 0
}

// parseShortCurl handles a short-flag token. Bundled boolean flags (-sSL) are
// expanded; a value-taking flag may carry its value attached (-ovalue) or as the
// next argument. Returns (consumed, ok): ok=false means "let real curl handle
// this whole invocation".
func parseShortCurl(tok string, args []string, i *int, method, url *string,
	setHeader func(string), body *[]byte, haveBody *bool, output *string,
	useRemote, include, headOnly, failFast *bool, timeout *time.Duration,
	headers map[string]string) (bool, bool) {

	// takeValue returns the remainder of the token as the value, or the next arg.
	chars := tok[1:]
	for idx := 0; idx < len(chars); idx++ {
		c := chars[idx]
		rest := chars[idx+1:]
		takeValue := func() (string, bool) {
			if rest != "" {
				return rest, true
			}
			if *i+1 >= len(args) {
				return "", false
			}
			*i++
			return args[*i], true
		}
		switch c {
		case 's', 'S', 'L', 'v':
			// silent / show-error / location / verbose — no-ops here.
		case 'f':
			*failFast = true
		case 'i':
			*include = true
		case 'I':
			*headOnly = true
		case 'O':
			*useRemote = true
		case 'X':
			v, ok := takeValue()
			if !ok {
				return false, false
			}
			*method = strings.ToUpper(v)
			return true, true
		case 'H':
			v, ok := takeValue()
			if !ok {
				return false, false
			}
			setHeader(v)
			return true, true
		case 'd':
			v, ok := takeValue()
			if !ok {
				return false, false
			}
			if strings.HasPrefix(v, "@") {
				data, err := os.ReadFile(v[1:])
				if err != nil {
					return false, false
				}
				v = string(data)
			}
			*body = append(*body, []byte(v)...)
			*haveBody = true
			return true, true
		case 'o':
			v, ok := takeValue()
			if !ok {
				return false, false
			}
			*output = v
			return true, true
		case 'A':
			v, ok := takeValue()
			if !ok {
				return false, false
			}
			headers["User-Agent"] = v
			return true, true
		case 'e':
			v, ok := takeValue()
			if !ok {
				return false, false
			}
			headers["Referer"] = v
			return true, true
		case 'b':
			v, ok := takeValue()
			if !ok {
				return false, false
			}
			headers["Cookie"] = v
			return true, true
		case 'u':
			v, ok := takeValue()
			if !ok {
				return false, false
			}
			headers["Authorization"] = "Basic " + base64.StdEncoding.EncodeToString([]byte(v))
			return true, true
		case 'm':
			v, ok := takeValue()
			if !ok {
				return false, false
			}
			*timeout = parseSeconds(v)
			return true, true
		default:
			// Any short flag we do not model (e.g. -F, -T, -x, -G, -w, -E).
			return false, false
		}
	}
	return true, true
}

// cutFlag splits "--name=value" into ("--name", "value", true) or
// ("--name", "", false).
func cutFlag(a string) (name, value string, hasValue bool) {
	if k, v, ok := strings.Cut(a, "="); ok {
		return k, v, true
	}
	return a, "", false
}

func lookupHeader(h map[string]string, name string) (string, bool) {
	for k, v := range h {
		if strings.EqualFold(k, name) {
			return v, true
		}
	}
	return "", false
}

func parseSeconds(s string) time.Duration {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	var f float64
	if _, err := fmt.Sscanf(s, "%f", &f); err != nil || f <= 0 {
		return 0
	}
	return time.Duration(f * float64(time.Second))
}
