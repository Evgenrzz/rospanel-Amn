package naive

import (
	"strings"
	"testing"
)

// deterministic placeholder material for the zero-users case
func fixedRand(t *testing.T) {
	t.Helper()
	old := randHex
	randHex = func() string { return strings.Repeat("ab", 20) }
	t.Cleanup(func() { randHex = old })
}

func baseConfig() Config {
	return Config{
		Port:     2053,
		Domain:   "vpn.example.com",
		CertFile: "/var/lib/rospanel-node/tls/fullchain.pem",
		KeyFile:  "/var/lib/rospanel-node/tls/privkey.pem",
		LogFile:  "/var/lib/rospanel-node/naive/access.log",
		Users: []User{
			{Name: "u7", Pass: "0123456789abcdef01234567"},
		},
		Masq: Masquerade{Kind: MasqFile, Dir: "/var/lib/rospanel-node/decoy"},
	}
}

// The rendered file must carry the structural invariants the reference
// deployment learned the hard way: forward_proxy ordered ahead of every
// handler, h1+h2 only (HTTP/3 off keeps UDP/443 for Hysteria and stops the
// clients HTTP/3 breaks), automatic HTTPS off with an explicit certificate,
// and the ACCESS log inside the site (a global log block would leave traffic
// accounting at zero).
func TestRenderStructure(t *testing.T) {
	t.Parallel()
	out, err := Render(baseConfig())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"order forward_proxy first",
		"protocols h1 h2",
		"auto_https off",
		"admin off",
		":2053, vpn.example.com:2053 {",
		"tls /var/lib/rospanel-node/tls/fullchain.pem /var/lib/rospanel-node/tls/privkey.pem",
		"output file /var/lib/rospanel-node/naive/access.log {",
		"roll_size 50mb",
		"roll_keep_for 720h",
		"format json",
		"basic_auth u7 0123456789abcdef01234567",
		"hide_ip",
		"hide_via",
		"file_server {",
		"root /var/lib/rospanel-node/decoy",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered Caddyfile is missing %q:\n%s", want, out)
		}
	}
	// forward_proxy must be written BEFORE the masquerade handler: the global
	// order directive covers evaluation, but the reference template keeps the
	// same textual order and caddy fmt expects handlers in site order.
	if strings.Index(out, "forward_proxy") > strings.Index(out, "file_server") {
		t.Errorf("masquerade handler precedes forward_proxy:\n%s", out)
	}
}

// Zero users must still render a credential line — a forward_proxy block
// without basic_auth entries would accept anyone — and it must be a random
// unreachable pair, regenerated on every render.
func TestRenderZeroUsersPlaceholder(t *testing.T) {
	fixedRand(t)
	cfg := baseConfig()
	cfg.Users = nil
	out, err := Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "basic_auth _placeholder_"+strings.Repeat("ab", 8)+" _disabled_"+strings.Repeat("ab", 12)) {
		t.Errorf("no placeholder credential line:\n%s", out)
	}
}

func TestRenderPortOnlySite(t *testing.T) {
	t.Parallel()
	cfg := baseConfig()
	cfg.Domain = ""
	out, err := Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, ":2053 {") {
		t.Errorf("port-only site missing:\n%s", out)
	}
}

func TestRenderNoLogFile(t *testing.T) {
	t.Parallel()
	cfg := baseConfig()
	cfg.LogFile = ""
	out, err := Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "output file") {
		t.Errorf("access log emitted despite LogFile empty:\n%s", out)
	}
}

func TestRenderMirrorMasquerade(t *testing.T) {
	t.Parallel()
	cfg := baseConfig()
	cfg.Masq = Masquerade{Kind: MasqMirror, URL: "https://www.mozilla.org"}
	out, err := Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"reverse_proxy https://www.mozilla.org {",
		"header_up Host www.mozilla.org",
		"tls_server_name www.mozilla.org",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("mirror masquerade missing %q:\n%s", want, out)
		}
	}
}

func TestRenderProbeAndUpstream(t *testing.T) {
	t.Parallel()
	cfg := baseConfig()
	cfg.ProbeSecret = "hidden.example.com"
	cfg.Upstream = "https://u1:p1@exit.example.com:8443"
	out, err := Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "probe_resistance hidden.example.com") {
		t.Errorf("probe_resistance missing:\n%s", out)
	}
	if !strings.Contains(out, "upstream https://u1:p1@exit.example.com:8443") {
		t.Errorf("upstream missing:\n%s", out)
	}
}

// Every string that lands in the file bare must be rejected when it could
// split into a second token or smuggle a directive.
func TestRenderRejectsInjection(t *testing.T) {
	t.Parallel()
	bad := map[string]Config{
		"username with space": func() Config {
			c := baseConfig()
			c.Users = []User{{Name: "u7 evil", Pass: "0123456789abcdef01234567"}}
			return c
		}(),
		"password with brace": func() Config {
			c := baseConfig()
			c.Users = []User{{Name: "u7", Pass: "0123456789abcde{"}}
			return c
		}(),
		"short password": func() Config {
			c := baseConfig()
			c.Users = []User{{Name: "u7", Pass: "abc"}}
			return c
		}(),
		"domain not a host": func() Config {
			c := baseConfig()
			c.Domain = "www.max.ru1"
			return c
		}(),
		"cert path with space": func() Config {
			c := baseConfig()
			c.CertFile = "/tmp/a b.pem"
			return c
		}(),
		"missing key": func() Config {
			c := baseConfig()
			c.KeyFile = ""
			return c
		}(),
		"bad port": func() Config {
			c := baseConfig()
			c.Port = 70000
			return c
		}(),
		"probe secret with newline": func() Config {
			c := baseConfig()
			c.ProbeSecret = "a\nadmin off"
			return c
		}(),
		"file masq without dir": func() Config {
			c := baseConfig()
			c.Masq = Masquerade{Kind: MasqFile}
			return c
		}(),
		"mirror URL not http": func() Config {
			c := baseConfig()
			c.Masq = Masquerade{Kind: MasqMirror, URL: "ftp://example.com"}
			return c
		}(),
		"upstream bare IP": func() Config {
			c := baseConfig()
			c.Upstream = "https://1.2.3.4:443"
			return c
		}(),
	}
	for name, cfg := range bad {
		if _, err := Render(cfg); err == nil {
			t.Errorf("%s: rendered without an error", name)
		}
	}
}

func TestNormalizeUpstream(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ in, want string }{
		{"https://exit.example.com", "https://exit.example.com"},
		{"http://exit.example.com", "https://exit.example.com"},
		{"naive+https://exit.example.com:8443", "https://exit.example.com:8443"},
		{"exit.example.com", "https://exit.example.com"},
		{"https://u:p@exit.example.com:8443", "https://u:p@exit.example.com:8443"},
	} {
		got, err := normalizeUpstream(c.in)
		if err != nil || got != c.want {
			t.Errorf("normalizeUpstream(%q) = %q, %v — want %q", c.in, got, err, c.want)
		}
	}
	for _, in := range []string{"https://1.2.3.4", "https://", "::::"} {
		if _, err := normalizeUpstream(in); err == nil {
			t.Errorf("normalizeUpstream(%q): no error", in)
		}
	}
}

func TestGeneratePassword(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for range 100 {
		p, err := GeneratePassword()
		if err != nil {
			t.Fatal(err)
		}
		if len(p) != PasswordLen || !passRe.MatchString(p) {
			t.Fatalf("generated %q outside the contract", p)
		}
		if seen[p] {
			t.Fatalf("generated a duplicate password within 100 draws: %q", p)
		}
		seen[p] = true
	}
}

func TestUserName(t *testing.T) {
	t.Parallel()
	if got := UserName(42); got != "u42" {
		t.Errorf("UserName(42) = %q", got)
	}
	if !userNameRe.MatchString(UserName(1 << 40)) {
		t.Error("a large id produced a username outside the character set")
	}
}

// The admin socket is how the supervisor reloads without dropping tunnels; it
// must land in the global block in caddy's unix address form, and a path that
// is not absolute single-token must be refused before it can split the file.
func TestRenderAdminSocket(t *testing.T) {
	t.Parallel()
	cfg := baseConfig()
	cfg.AdminSocket = "/var/lib/rospanel-node/naive/admin.sock"
	out, err := Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "admin unix//var/lib/rospanel-node/naive/admin.sock") {
		t.Errorf("admin socket not rendered:\n%s", out)
	}
	if strings.Contains(out, "admin off") {
		t.Errorf("admin off rendered next to a socket:\n%s", out)
	}

	cfg.AdminSocket = "relative/admin.sock"
	if _, err := Render(cfg); err == nil {
		t.Error("a relative admin socket path rendered without an error")
	}
}

// The node's decoy listens on loopback, and mirroring it is the lane's
// masquerade: a loopback IP must be accepted as a mirror host even though it
// is not a domain.
func TestRenderLoopbackMirror(t *testing.T) {
	t.Parallel()
	cfg := baseConfig()
	cfg.Masq = Masquerade{Kind: MasqMirror, URL: "http://127.0.0.1:8080"}
	out, err := Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "reverse_proxy http://127.0.0.1:8080") {
		t.Errorf("loopback mirror not rendered:\n%s", out)
	}
	// An http mirror needs no TLS transport block.
	if strings.Contains(out, "tls_server_name") {
		t.Errorf("an http mirror rendered a TLS transport:\n%s", out)
	}

	cfg.Masq = Masquerade{Kind: MasqMirror, URL: "http://203.0.113.5:8080"}
	if _, err := Render(cfg); err == nil {
		t.Error("a non-loopback IP mirror rendered without an error")
	}
}
