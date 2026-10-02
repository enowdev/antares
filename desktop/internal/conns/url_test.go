package conns

import "testing"

func TestValidateURL(t *testing.T) {
	ok := map[string]string{
		"https://antares.example.com":       "https://antares.example.com",
		"https://antares.example.com/":      "https://antares.example.com",
		"antares.example.com":               "https://antares.example.com",
		"HTTPS://Antares.Example.com:8443/": "https://antares.example.com:8443",
		"https://example.com/antares/":      "https://example.com/antares",
		"http://localhost:8787":             "http://localhost:8787",
		"http://127.0.0.1:8787":             "http://127.0.0.1:8787",
		"http://[::1]:8787":                 "http://[::1]:8787",
		"http://10.1.2.3":                   "http://10.1.2.3",
		"http://172.16.0.9:8787":            "http://172.16.0.9:8787",
		"http://172.31.255.1":               "http://172.31.255.1",
		"http://192.168.1.20:8787":          "http://192.168.1.20:8787",
		"http://169.254.10.1":               "http://169.254.10.1",
		"http://100.64.0.1:8787":            "http://100.64.0.1:8787",
		"http://100.127.255.254":            "http://100.127.255.254",
		"http://box.tail1234.ts.net:8787":   "http://box.tail1234.ts.net:8787",
		"http://[fd7a:115c:a1e0::1]:8787":   "http://[fd7a:115c:a1e0::1]:8787",
		"  https://padded.example.com  ":    "https://padded.example.com",
	}
	for in, want := range ok {
		got, err := ValidateURL(in)
		if err != nil || got != want {
			t.Errorf("ValidateURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	bad := []string{
		"",
		"http://example.com",
		"http://8.8.8.8",
		"http://100.128.0.1", // just outside Tailscale's /10
		"http://172.32.0.1",  // just outside 172.16/12
		"http://ts.net.evil.com",
		"http://evil-ts.net",
		"ftp://antares.example.com",
		"https://user:pw@antares.example.com",
		"https://antares.example.com/?x=1",
		"https://antares.example.com/#frag",
		"https:///nohost",
	}
	for _, in := range bad {
		if got, err := ValidateURL(in); err == nil {
			t.Errorf("ValidateURL(%q) = %q, want error", in, got)
		}
	}
}
