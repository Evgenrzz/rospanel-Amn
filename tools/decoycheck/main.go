// decoycheck is the local verification harness for the CMS persona layer.
//
// It runs the same class of checks an inexpensive scanner runs against a live
// node (the manual32 "self-steal" rubric): robots.txt and its promises, the
// sitemap's entries, every reference a persona page emits, the engine
// signatures, the guessed-path 404, the method gate and the Content-Length
// rule — in-process, over a real HTTP stack, for every persona template.
//
//	go run ./tools/decoycheck                        # probe matrix, exit 1 on any dead promise
//	go run ./tools/decoycheck -serve 10gag -addr localhost:8443
//	                                                 # live self-signed server to poke by hand:
//	  curl -k https://localhost:8443/robots.txt
//	  curl -k -I https://localhost:8443/wp-admin/
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"flag"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/decoy"
)

// personaTemplates are the slugs the installer picks from, plus tonari (the
// manually-selectable DLE portal). filecloud rides the static persona.
var personaTemplates = []string{"10gag", "YouTube", "speedtest", "downloader", "converter", "nextcloud", "filecloud", "tonari-no-totoro"}

type probeEP struct {
	path string
	want int
}

// engineProbes are the per-persona signatures: status codes taken from the real
// software. Any deviation means the persona layer regressed.
var engineProbes = map[string][]probeEP{
	"10gag": {
		{"/wp-admin", 301}, {"/wp-admin/", 302}, {"/xmlrpc.php", 405}, {"/xmlrpc.php?rsd", 200},
		{"/wp-login.php", 200}, {"/wp-login.php?action=lostpassword", 200}, {"/feed/", 200},
		{"/comments/feed/", 200}, {"/readme.html", 200}, {"/wp-json/", 200},
		{"/wp-includes/wlwmanifest.xml", 200}, {"/wp-includes/js/wp-polyfill.min.js", 200},
		{"/wp-includes/images/blank.gif", 200}, {"/wp-includes/version.php", 403},
		{"/wp-admin/admin-ajax.php", 400}, {"/wp-cron.php", 200}, {"/sitemap.xml", 200},
	},
	"YouTube": {
		{"/administrator", 301}, {"/administrator/", 200}, {"/administrator/index.php", 200},
		{"/cache/", 403}, {"/components/", 403}, {"/modules/", 403}, {"/plugins/", 403}, {"/sitemap.xml", 200},
	},
	"speedtest": {
		{"/user", 302}, {"/user/login", 200}, {"/core/", 403}, {"/modules/", 403},
		{"/themes/", 403}, {"/rss.xml", 200}, {"/search/node", 200}, {"/sitemap.xml", 200},
	},
	"downloader": {
		{"/bitrix/", 403}, {"/upload/", 403}, {"/auth/", 200}, {"/personal/", 302}, {"/sitemap.xml", 200},
	},
	"converter": {
		{"/admin.php", 200}, {"/engine/", 403}, {"/statistics.html", 200}, {"/index.php", 200},
		{"/index.php?do=rss", 200}, {"/index.php?do=opensearch", 200}, {"/login.php", 200}, {"/sitemap.xml", 200},
	},
	"tonari-no-totoro": {
		{"/admin.php", 200}, {"/engine/", 403}, {"/statistics.html", 200}, {"/index.php", 200},
		{"/index.php?do=rss", 200}, {"/index.php?do=opensearch", 200}, {"/login.php", 200},
		{"/sitemap.xml", 200}, {"/templates/one/player.html", 200}, {"/templates/one/player2.html", 200},
	},
	"nextcloud": {
		{"/status.php", 200}, {"/login", 303}, {"/index.php/login", 200},
		{"/index.php/apps/settings", 303}, {"/remote.php/dav", 401}, {"/ocs/v2.php", 401}, {"/.well-known/caldav", 301},
	},
	"filecloud": {{"/sitemap.xml", 200}, {"/index.php", 200}},
}

// crawlSeeds are the persona pages whose emitted references must all resolve.
var crawlSeeds = map[string][]string{
	"10gag":            {"/", "/feed/", "/comments/feed/", "/xmlrpc.php?rsd", "/wp-login.php", "/wp-login.php?action=lostpassword", "/readme.html", "/wp-includes/wlwmanifest.xml", "/wp-json/", "/sitemap.xml", "/robots.txt"},
	"YouTube":          {"/", "/administrator/", "/sitemap.xml", "/robots.txt"},
	"speedtest":        {"/", "/rss.xml", "/user/login", "/search/node", "/sitemap.xml", "/robots.txt"},
	"downloader":       {"/", "/auth/", "/sitemap.xml", "/robots.txt"},
	"converter":        {"/", "/index.php?do=rss", "/index.php?do=opensearch", "/admin.php", "/statistics.html", "/sitemap.xml", "/robots.txt"},
	"tonari-no-totoro": {"/", "/index.php?do=rss", "/admin.php", "/statistics.html", "/sitemap.xml", "/robots.txt", "/templates/one/player.html"},
	"nextcloud":        {"/", "/status.php", "/index.php/login", "/robots.txt"},
	"filecloud":        {"/", "/sitemap.xml", "/robots.txt"},
}

var (
	refAttr = regexp.MustCompile(`(?i)(?:href|src|action|template|apiLink|pingback|logo)\s*=\s*"([^"]+)"`)
	locTag  = regexp.MustCompile(`<loc>([^<]+)</loc>`)
)

type report struct {
	checks, fails int
}

func (r *report) check(name string, ok bool, detail string) {
	r.checks++
	if !ok {
		r.fails++
		fmt.Printf("  FAIL %-60s %s\n", name, detail)
	}
}

func main() {
	serve := flag.String("serve", "", "serve one template live over a self-signed cert instead of running the matrix")
	addr := flag.String("addr", "localhost:8443", "listen address for -serve")
	flag.Parse()

	if *serve != "" {
		liveServer(*serve, *addr)
		return
	}

	var total report
	for _, tmpl := range personaTemplates {
		fmt.Printf("== %s ==\n", tmpl)
		rep := probeTemplate(tmpl)
		fmt.Printf("  %d checks, %d failures\n", rep.checks, rep.fails)
		total.checks += rep.checks
		total.fails += rep.fails
	}
	fmt.Printf("\nTOTAL: %d checks, %d failures\n", total.checks, total.fails)
	if total.fails > 0 {
		os.Exit(1)
	}
}

func newHandler(tmpl string) *decoy.Handler {
	dir, err := os.MkdirTemp("", "decoycheck")
	if err != nil {
		panic(err)
	}
	h, err := decoy.New(tmpl, decoy.LoadStamp(dir))
	if err != nil {
		panic(err)
	}
	return h
}

func probeTemplate(tmpl string) report {
	var rep report
	srv := httptest.NewServer(newHandler(tmpl))
	defer srv.Close()
	base := srv.URL
	// Redirects stay unfollowed: the persona's Location headers name the
	// request's host, and a probe must see the hop itself, not chase it.
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}

	get := func(p string) (*http.Response, string) {
		resp, err := client.Get(base + p)
		if err != nil {
			rep.check("GET "+p, false, err.Error())
			return nil, ""
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		return resp, string(body)
	}

	// 1. Front page: full static validators, Caddy banner, HSTS, no Alt-Svc.
	resp, indexBody := get("/")
	if resp == nil {
		return rep
	}
	rep.check("front page 200", resp.StatusCode == 200, resp.Status)
	rep.check("ETag + Last-Modified + Content-Length",
		resp.Header.Get("Etag") != "" && resp.Header.Get("Last-Modified") != "" && resp.ContentLength > 0, "")
	rep.check("Server: Caddy", resp.Header.Get("Server") == "Caddy", resp.Header.Get("Server"))
	rep.check("HSTS present", resp.Header.Get("Strict-Transport-Security") != "", "")
	rep.check("no Alt-Svc", resp.Header.Get("Alt-Svc") == "", resp.Header.Get("Alt-Svc"))

	// 2. robots.txt: every named path answers.
	resp, robotsBody := get("/robots.txt")
	if resp == nil {
		return rep
	}
	rep.check("robots.txt 200", resp.StatusCode == 200, resp.Status)
	for _, line := range strings.Split(robotsBody, "\n") {
		line = strings.TrimSpace(line)
		var raw string
		switch {
		case strings.HasPrefix(line, "Disallow: "):
			raw = strings.TrimPrefix(line, "Disallow: ")
		case strings.HasPrefix(line, "Allow: "):
			raw = strings.TrimPrefix(line, "Allow: ")
		case strings.HasPrefix(line, "Sitemap: "):
			raw = strings.TrimPrefix(line, "Sitemap: ")
			if i := strings.Index(raw, "/sitemap"); i >= 0 {
				raw = raw[i:]
			}
		default:
			continue
		}
		if i := strings.IndexAny(raw, "*?"); i >= 0 {
			raw = raw[:i]
		}
		if raw == "" || raw == "/" || !strings.HasPrefix(raw, "/") {
			continue
		}
		if r2, _ := get(raw); r2 != nil {
			rep.check("robots path "+raw, r2.StatusCode != 404, "404 — dead promise")
		}
	}

	// 3. Engine signatures per persona.
	for _, p := range engineProbes[tmpl] {
		r2, _ := get(p.path)
		if r2 != nil {
			rep.check("endpoint "+p.path, r2.StatusCode == p.want, fmt.Sprintf("got %d want %d", r2.StatusCode, p.want))
		}
	}

	// 4. Guessed path: CMSes 404 with a body that is not the front page.
	r3, missBody := get("/definitely-not-a-real-page-x7")
	if r3 != nil {
		if tmpl == "filecloud" {
			rep.check("SPA try_files fallback stays 200", r3.StatusCode == 200, fmt.Sprint(r3.StatusCode))
		} else {
			rep.check("guess 404s (notfound=404)", r3.StatusCode == 404, fmt.Sprint(r3.StatusCode))
			rep.check("404 body differs from front page", strings.TrimSpace(missBody) != strings.TrimSpace(indexBody), "")
		}
	}

	// 5. Every same-host reference the persona pages emit resolves.
	seen := map[string]bool{}
	for _, seed := range crawlSeeds[tmpl] {
		r4, body4 := get(seed)
		if r4 == nil || r4.StatusCode != 200 {
			rep.check("seed "+seed, r4 != nil && r4.StatusCode == 200, "seed must answer 200")
			continue
		}
		ct := r4.Header.Get("Content-Type")
		if !strings.Contains(ct, "html") && !strings.Contains(ct, "xml") {
			continue
		}
		var refs []string
		refs = append(refs, allSubmatches(refAttr, body4)...)
		refs = append(refs, allSubmatches(locTag, body4)...)
		for _, ref := range refs {
			if strings.HasPrefix(ref, "//") {
				rep.check(seed+" ref "+ref, false, "protocol-relative — hidden third-party load")
				continue
			}
			if !strings.HasPrefix(ref, "/") {
				continue // external anchors, mailto:, data:
			}
			ref = strings.SplitN(strings.SplitN(ref, "#", 2)[0], "&amp;", 2)[0]
			ref = strings.ReplaceAll(ref, "%%", "%")
			if ref == "" || strings.ContainsAny(ref, " \t\r\n\"'<>{}") || seen[seed+ref] {
				continue
			}
			seen[seed+ref] = true
			if r5, _ := get(ref); r5 != nil {
				rep.check(seed+" → "+ref, r5.StatusCode != 404, "404 — dead reference")
			}
		}
	}

	// 6. Sitemap entries are live (nextcloud advertises none by design).
	if tmpl != "nextcloud" {
		r6, body6 := get("/sitemap.xml")
		if r6 != nil && r6.StatusCode == 200 {
			n := 0
			for _, m := range allSubmatches(locTag, body6) {
				p := m
				if strings.HasPrefix(p, base) {
					p = strings.TrimPrefix(p, base)
				} else if i := strings.Index(p, "://"); i >= 0 {
					rest := p[i+3:]
					if j := strings.Index(rest, "/"); j >= 0 {
						p = rest[j:]
					} else {
						p = "/"
					}
				}
				if !strings.HasPrefix(p, "/") {
					continue
				}
				n++
				if r7, _ := get(p); r7 != nil {
					rep.check("sitemap <loc> live "+p, r7.StatusCode/100 == 2, fmt.Sprint(r7.StatusCode))
				}
			}
			rep.check("sitemap has entries", n > 0, "no <loc> rows")
		} else if r6 != nil {
			rep.check("sitemap.xml answers", false, r6.Status)
		}
	}

	// 7. xmlrpc POST answers like WordPress core, not like a file server 405.
	if tmpl == "10gag" {
		req, _ := http.NewRequest(http.MethodPost, base+"/xmlrpc.php",
			strings.NewReader("<methodCall><methodName>system.listMethods</methodName></methodCall>"))
		resp8, err := srv.Client().Do(req)
		if err == nil {
			body8, _ := io.ReadAll(io.LimitReader(resp8.Body, 1<<20))
			resp8.Body.Close()
			rep.check("xmlrpc POST listMethods",
				resp8.StatusCode == 200 && strings.Contains(string(body8), "blogger.getUsersBlogs"), resp8.Status)
		}
	}
	return rep
}

func allSubmatches(re *regexp.Regexp, s string) []string {
	var out []string
	for _, m := range re.FindAllStringSubmatch(s, -1) {
		out = append(out, m[1])
	}
	return out
}

// liveServer serves one template over HTTPS with a throwaway self-signed cert,
// for hands-on curling; in production TLS is Xray's, this is only the harness.
func liveServer(tmpl, addr string) {
	h := newHandler(tmpl)
	cert, err := selfSignedCert()
	if err != nil {
		panic(err)
	}
	srv := &http.Server{
		Addr:      addr,
		Handler:   h,
		TLSConfig: &tls.Config{Certificates: []tls.Certificate{cert}},
	}
	fmt.Printf("decoy %q is live on https://%s — probe it with curl -k\n\n", tmpl, addr)
	for _, p := range []string{"/robots.txt", "/sitemap.xml", "/wp-admin/", "/xmlrpc.php", "/wp-login.php", "/readme.html", "/no-such-page"} {
		fmt.Printf("  curl -k -o NUL -w \"%%{http_code} %%{content_type}\\n\" https://%s%s\n", addr, p)
	}
	if err := srv.ListenAndServeTLS("", ""); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func selfSignedCert() (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	tpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "localhost"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * 365 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, nil
}
