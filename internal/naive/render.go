package naive

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
)

// Render writes the Caddyfile for one lane configuration. It is the port of
// the reference deployment template (RIXXX panel's caddyTemplate.js) with the
// differences rospanel's node layout forces:
//
//   - TLS material is handed over instead of requested: the node already runs
//     the panel's TLS front on 443/80, so caddy's automatic HTTPS is OFF and
//     the site carries `tls <cert> <key>` from the panel's store. There is no
//     :80 redirect site either — port 80 belongs to the panel.
//   - No panel/subscription site blocks: this caddy is only the tunnel.
//
// Everything else follows the reference exactly, bug-fix annotations and all:
// forward_proxy is ordered first so the masquerade handler (file OR mirror)
// can never hijack an authenticated CONNECT; protocols are pinned to h1+h2
// because NaiveProxy tunnels over HTTP/2 CONNECT and HTTP/3 breaks clients
// (which also keeps UDP/443 free for the Hysteria lane); and the access log
// is a SITE-level JSON log, because a global one configures only the runtime
// logger and would leave traffic accounting at zero.
func Render(cfg Config) (string, error) {
	if err := cfg.Validate(); err != nil {
		return "", err
	}
	var b strings.Builder

	// Global options.
	b.WriteString(`{
	order forward_proxy first
	servers {
		protocols h1 h2
	}
	auto_https off
	admin off
	log {
		output stderr
		format console
		level ERROR
	}
}

`)

	// Site address: the bare port first, then the domain. ":<port>" must lead
	// for the CONNECT to match however the client sets SNI/Host (the same
	// reason the reference template lists ":443, example.com").
	site := fmt.Sprintf(":%d", cfg.Port)
	if cfg.Domain != "" {
		site += ", " + cfg.Domain
	}
	fmt.Fprintf(&b, "%s {\n\ttls %s %s\n", site, cfg.CertFile, cfg.KeyFile)

	// Site-level access log: one JSON line per handled request, carrying
	// request.user_id and the byte counters the panel's traffic accounting
	// reads. Rotation by age (30 days) rather than count, so a busy lane and
	// an idle one keep the same window.
	if cfg.LogFile != "" {
		fmt.Fprintf(&b, `
	log {
		output file %s {
			roll_size 50mb
			roll_keep_for 720h
		}
		format json
	}
`, cfg.LogFile)
	}

	// Credentials, then the privacy/probe directives in the order the plugin
	// documents. With zero users a random unreachable pair stands in: an
	// empty forward_proxy block would mean the tunnel takes anyone.
	b.WriteString("\n\tforward_proxy {\n")
	if len(cfg.Users) == 0 {
		rnd := randHex()
		fmt.Fprintf(&b, "\t\tbasic_auth _placeholder_%s _disabled_%s\n", rnd[:16], rnd[16:])
	}
	for _, u := range cfg.Users {
		fmt.Fprintf(&b, "\t\tbasic_auth %s %s\n", u.Name, u.Pass)
	}
	b.WriteString("\t\thide_ip\n\t\thide_via")
	if cfg.ProbeSecret != "" {
		fmt.Fprintf(&b, "\n\t\tprobe_resistance %s", cfg.ProbeSecret)
	}
	if cfg.Upstream != "" {
		up, _ := normalizeUpstream(cfg.Upstream) // validated by Validate
		fmt.Fprintf(&b, "\n\t\tupstream %s", up)
	}
	b.WriteString("\n\t}\n")

	// Masquerade: what everyone who is not an authenticated client sees.
	switch cfg.Masq.Kind {
	case MasqFile:
		fmt.Fprintf(&b, "\n\tfile_server {\n\t\troot %s\n\t}\n", cfg.Masq.Dir)
	case MasqMirror:
		scheme, host, _ := parseMirrorURL(cfg.Masq.URL) // validated by Validate
		fmt.Fprintf(&b, "\n\treverse_proxy %s://%s {\n\t\theader_up Host %s\n", scheme, host, host)
		if scheme == "https" {
			fmt.Fprintf(&b, "\t\ttransport http {\n\t\t\ttls\n\t\t\ttls_server_name %s\n\t\t}\n", host)
		}
		b.WriteString("\t}\n")
	}

	b.WriteString("}\n")
	return b.String(), nil
}

// randHex is 20 random bytes as hex — the placeholder credential material. A
// var so tests can make Render deterministic.
var randHex = func() string {
	b := make([]byte, 20)
	_, _ = rand.Read(b) // crypto/rand.Read never fails on a live platform
	return hex.EncodeToString(b)
}

// normalizeUpstream cleans a cascade target into the one form forward_proxy's
// `upstream` accepts: a plain https:// URL. A naive+https://… link pasted by
// an operator is unwrapped, and a bare host or http:// is upgraded — matching
// the reference template, which had to learn this the hard way (Bug 92).
func normalizeUpstream(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", nil
	}
	if i := strings.Index(s, "+"); i > 0 && strings.HasPrefix(s[i+1:], "http") {
		s = s[i+1:]
	}
	if strings.HasPrefix(s, "http://") {
		s = "https://" + s[len("http://"):]
	}
	if !strings.HasPrefix(s, "https://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" || !hostRe.MatchString(u.Hostname()) {
		return "", fmt.Errorf("naive: upstream %q is not an https:// proxy URL with a real hostname", raw)
	}
	return s, nil
}

// parseMirrorURL splits a masquerade-mirror URL into scheme and host[:port].
// Only real http(s) URLs qualify; anything else is a configuration mistake,
// not a reason to silently fall back to no camouflage.
func parseMirrorURL(raw string) (scheme, host string, err error) {
	s := strings.TrimSpace(raw)
	u, perr := url.Parse(s)
	if perr != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || !hostRe.MatchString(u.Hostname()) {
		return "", "", fmt.Errorf("naive: mirror URL %q is not an http(s) URL with a real hostname", raw)
	}
	return u.Scheme, u.Host, nil
}
