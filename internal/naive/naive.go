// Package naive implements the NaiveProxy lane: a Caddy server built with the
// klzgrad forwardproxy-naive plugin (the "caddy-naive" binary) fronting an
// HTTP/2 CONNECT tunnel whose traffic is indistinguishable from a Chrome
// browser talking to a real website.
//
// The lane follows the AmneziaWG one (internal/awg): the panel decides the
// configuration — port, TLS material, per-user credentials, masquerade — and
// hands it to whatever runs the tunnel (the master process itself or a node
// agent) as a plain value. This package owns the three pieces that are pure
// functions of that decision and therefore testable anywhere:
//
//   - Render: the Caddyfile the server runs with (ported from the reference
//     deployment template, with the panel owning TLS instead of ACME);
//   - the access-log reader: caddy's site-level JSON log is the ONLY traffic
//     accounting NaiveProxy has — there is no API to poll like Xray's;
//   - EnsureBinary: the pinned server binary, downloaded and checksum-gated
//     exactly like internal/xray does for Xray itself.
//
// Credentials are basic-auth pairs baked into the Caddyfile: a user is
// u<id> with a generated password, so a reload is all it takes to add or
// revoke one. The file holds every user's plaintext password, which is why
// the caller must keep it 0600 — caddy hashes them internally, but nothing
// upstream of it does.
package naive

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"regexp"
	"strconv"
)

// User is one basic-auth credential pair served by the tunnel. Name is the
// username clients put in their naive+secure:// link (UserName below); Pass is
// the plaintext password — the only form caddy's forward_proxy accepts.
type User struct {
	Name string
	Pass string
}

// MasqKind selects what an unauthenticated visitor (a censor's probe, a
// scanner, a browser pointed at the port by mistake) sees.
type MasqKind int

const (
	// MasqNone answers with an empty 200 — the site block has no handler
	// besides forward_proxy. Acceptable when the port is not 443 and the
	// operator relies on probe_resistance instead.
	MasqNone MasqKind = iota
	// MasqFile serves a static directory (the panel's decoy site).
	MasqFile
	// MasqMirror reverse-proxies a live website, Host header and SNI
	// rewritten to it, so the cover page is a real page.
	MasqMirror
)

// Config is everything the rendered Caddyfile depends on. It carries no
// secrets beyond the user passwords, which the caller stores encrypted at
// rest and decrypts only to render.
type Config struct {
	// Port is the TCP port the tunnel listens on. The panel fronts 443/80
	// with its own TLS front, so the lane defaults to a high port (2053).
	Port int
	// Domain is the hostname clients connect to; it is listed next to the
	// bare port in the site address so the CONNECT matches however the
	// client sets SNI/Host. Empty renders a port-only site.
	Domain string
	// CertFile and KeyFile are the TLS material the panel hands over (its
	// own ACME/self-signed store). Caddy never runs ACME for this lane:
	// auto_https is off and the certificate is explicit.
	CertFile string
	KeyFile  string
	// LogFile is the site-level JSON access log — the traffic-accounting
	// source (see accesslog.go). Empty disables access logging, which also
	// disables per-user traffic for the lane.
	LogFile string
	// Users are the credentials the tunnel accepts. Empty renders a random
	// unreachable placeholder so the forward_proxy block is never left
	// without credentials (which would mean "no auth required").
	Users []User
	// Masq is what unauthenticated visitors get.
	Masq Masquerade
	// ProbeSecret enables probe_resistance. Empty means the directive is
	// not emitted at all; a non-empty secret emits `probe_resistance
	// <secret>`, where <secret> is the hidden domain that IS served the
	// masquerade content while everything else gets an empty response.
	// The bare form (resistance without a secret domain) has no use here:
	// the panel's masquerade is served to everyone by default anyway.
	ProbeSecret string
	// Upstream cascades the tunnel through another HTTPS proxy
	// (https://user:pass@host:port). Empty exits directly.
	Upstream string
}

// Masquerade is the cover site served to anyone who is not an authenticated
// naive client.
type Masquerade struct {
	Kind MasqKind
	// Dir is the static site root (MasqFile).
	Dir string
	// URL is the mirrored live site, http(s)://host[:port] (MasqMirror).
	URL string
}

// userNameRe matches the generated usernames (u<id>) and anything an operator
// could have imported from an older deployment. Underscore and dash are in
// because the placeholder credentials use them.
var userNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// passRe matches generated passwords (alphanumeric). A narrower set than
// caddy would accept on purpose: the password also travels inside a
// naive+secure:// URL userinfo, where anything outside alphanumerics would
// need escaping rules that differ between clients.
var passRe = regexp.MustCompile(`^[A-Za-z0-9]{16,64}$`)

// hostRe matches a real domain name (at least one dot, alphabetic TLD) — the
// same shape the REALITY donor check uses. The lane's Domain and a mirror URL
// host must be real hostnames: they end up as caddy site addresses and SNI.
var hostRe = regexp.MustCompile(`^([a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?\.)+[a-zA-Z]{2,}$`)

// probeSecretRe is the hidden-domain token for probe_resistance.
var probeSecretRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,63}$`)

// pathTokenRe matches filesystem paths that are safe to emit as a bare
// Caddyfile token: no whitespace (the token separator), no quotes/backticks
// (Caddyfile quoting), no braces/commas (block and site-address syntax).
// Caddyfiles are read by caddy itself, but a value that could terminate its
// token early could also smuggle directives — the panel's own paths never
// contain these, so rejecting them costs nothing.
var pathTokenRe = regexp.MustCompile(`^[^\s"{}\x60,]+$`)

// UserName is the username of a user's naive credential. Derived from the id
// like the AWG slot is: stable across servers and regenerations of anything
// else, and readable in the access log without a join.
func UserName(userID int64) string { return "u" + strconv.FormatInt(userID, 10) }

// passAlphabet is the generated-password alphabet: letters and digits only
// (see passRe for why).
const passAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// PasswordLen is the generated password length — 24 alphanumeric characters
// is ~142 bits, far past brute force, and short enough to survive being
// typed by hand into a client that will not import a link.
const PasswordLen = 24

// GeneratePassword returns a fresh credential password.
func GeneratePassword() (string, error) {
	out := make([]byte, PasswordLen)
	limit := big.NewInt(int64(len(passAlphabet)))
	for i := range out {
		n, err := rand.Int(rand.Reader, limit)
		if err != nil {
			return "", err
		}
		out[i] = passAlphabet[n.Int64()]
	}
	return string(out), nil
}

// Validate reports whether the configuration can be rendered into a Caddyfile
// that means what it says. Every string that lands in the file is checked
// against the character set that keeps it a single token — the renderer emits
// values bare, and a value with a space in it would silently split into two
// directives.
func (c Config) Validate() error {
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("naive: port %d outside 1–65535", c.Port)
	}
	if c.Domain != "" && !hostRe.MatchString(c.Domain) {
		return fmt.Errorf("naive: domain %q is not a hostname", c.Domain)
	}
	for _, p := range []struct{ what, v string }{
		{"certificate path", c.CertFile},
		{"key path", c.KeyFile},
		{"access log path", c.LogFile},
		{"masquerade directory", masqDir(c)},
	} {
		if p.v != "" && !pathTokenRe.MatchString(p.v) {
			return fmt.Errorf("naive: %s %q contains characters a Caddyfile token cannot carry", p.what, p.v)
		}
	}
	if c.CertFile == "" || c.KeyFile == "" {
		return fmt.Errorf("naive: TLS material is required — the panel hands its certificate to the lane")
	}
	for _, u := range c.Users {
		if !userNameRe.MatchString(u.Name) {
			return fmt.Errorf("naive: username %q outside the allowed character set", u.Name)
		}
		if !passRe.MatchString(u.Pass) {
			return fmt.Errorf("naive: password for %q outside the allowed character set (16–64 alphanumeric)", u.Name)
		}
	}
	if c.ProbeSecret != "" && !probeSecretRe.MatchString(c.ProbeSecret) {
		return fmt.Errorf("naive: probe secret %q outside the allowed character set", c.ProbeSecret)
	}
	if c.Upstream != "" {
		if _, err := normalizeUpstream(c.Upstream); err != nil {
			return err
		}
	}
	switch c.Masq.Kind {
	case MasqNone:
	case MasqFile:
		if c.Masq.Dir == "" {
			return fmt.Errorf("naive: masquerade directory is empty")
		}
	case MasqMirror:
		if _, _, err := parseMirrorURL(c.Masq.URL); err != nil {
			return err
		}
	default:
		return fmt.Errorf("naive: unknown masquerade kind %d", c.Masq.Kind)
	}
	return nil
}

// masqDir is the directory a file masquerade serves, or "" for every other kind.
func masqDir(c Config) string {
	if c.Masq.Kind == MasqFile {
		return c.Masq.Dir
	}
	return ""
}
