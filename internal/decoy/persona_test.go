package decoy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// The persona layer promises a CMS. These tests hold it to that promise: every
// path robots.txt names must answer, every URL the persona's own pages emit must
// resolve, the engine signatures (redirects, status codes, generator tags) must
// match the real thing, and an unknown path must behave the way that engine
// behaves — a 404 on a CMS, the app shell on a genuine SPA. A scanner does not
// read the prose; it walks the promises and flags the first one that breaks.

var personaTemplates = map[string]personaID{
	"filecloud":        pStatic,
	"downloader":       pBitrix,
	"converter":        pDLE,
	"speedtest":        pDrupal,
	"10gag":            pWordPress,
	"YouTube":          pJoomla,
	"nextcloud":        pNextcloud,
	"tonari-no-totoro": pDLE,
}

// personaSeeds are the pages each engine serves in its own voice. They are
// fetched, scanned for references, and every same-host reference must resolve.
var personaSeeds = map[personaID][]string{
	pWordPress: {"/", "/robots.txt", "/sitemap.xml", "/feed/", "/comments/feed/", "/xmlrpc.php?rsd", "/wp-login.php", "/wp-login.php?action=lostpassword", "/readme.html", "/wp-includes/wlwmanifest.xml", "/wp-json/"},
	pJoomla:    {"/", "/robots.txt", "/sitemap.xml", "/administrator/"},
	pDrupal:    {"/", "/robots.txt", "/sitemap.xml", "/rss.xml", "/user/login"},
	pBitrix:    {"/", "/robots.txt", "/sitemap.xml", "/auth/"},
	pDLE:       {"/", "/robots.txt", "/sitemap.xml", "/index.php?do=rss", "/index.php?do=opensearch", "/admin.php", "/statistics.html"},
	pNextcloud: {"/", "/robots.txt", "/status.php", "/index.php/login"},
	pStatic:    {"/", "/robots.txt", "/sitemap.xml"},
}

func getHost(h *Handler, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "https://example.com"+target, nil))
	return rec
}

// 1. The persona follows from the template slug — the claim must match the look.
func TestPersonaTiedToTemplate(t *testing.T) {
	for tmpl, want := range personaTemplates {
		h := newTestHandler(t, tmpl)
		if h.persona != want {
			t.Errorf("%s: persona %v, want %v", tmpl, h.persona, want)
		}
	}
}

// 2. robots.txt is a promise list. Every Disallow/Allow path and the Sitemap
// line must be answered — a disallowed path that 404s says "WordPress" while
// the box says "nothing".
func TestRobotsOnlyNamesLivePaths(t *testing.T) {
	for tmpl := range personaTemplates {
		t.Run(tmpl, func(t *testing.T) {
			h := newTestHandler(t, tmpl)
			robots := getHost(h, "/robots.txt")
			if robots.Code != http.StatusOK {
				t.Fatalf("robots.txt status = %d", robots.Code)
			}
			for _, line := range strings.Split(robots.Body.String(), "\n") {
				line = strings.TrimSpace(line)
				var raw string
				switch {
				case strings.HasPrefix(line, "Disallow: "):
					raw = strings.TrimPrefix(line, "Disallow: ")
				case strings.HasPrefix(line, "Allow: "):
					raw = strings.TrimPrefix(line, "Allow: ")
				case strings.HasPrefix(line, "Sitemap: "):
					raw = strings.TrimPrefix(line, "Sitemap: ")
					raw = strings.TrimPrefix(raw, "https://example.com")
				default:
					continue
				}
				if raw == "" || raw == "/" {
					continue
				}
				// Cut wildcards and queries: /*?newsid= only promises the prefix.
				if i := strings.IndexAny(raw, "*"); i >= 0 {
					raw = raw[:i]
				}
				if i := strings.Index(raw, "?"); i >= 0 {
					raw = raw[:i]
				}
				if raw == "" || raw == "/" {
					continue
				}
				if rec := getHost(h, raw); rec.Code == http.StatusNotFound {
					t.Errorf("robots.txt names %q but it 404s — the dead promise this whole layer exists to kill", raw)
				}
			}
		})
	}
}

// 3. Every same-host reference the persona's pages emit must resolve. This
// covers the template's own markup, the injected head links, the login page's
// stylesheet and script, the feeds' self URLs and the sitemap's <loc> set.
func TestPersonaReferencesResolve(t *testing.T) {
	attr := regexp.MustCompile(`(?i)(?:href|src|action|template|apiLink|pingback|logo|bgimage)\s*=\s*"([^"]+)"`)
	loc := regexp.MustCompile(`<loc>([^<]+)</loc>`)

	for tmpl := range personaTemplates {
		t.Run(tmpl, func(t *testing.T) {
			h := newTestHandler(t, tmpl)
			for _, seed := range personaSeeds[h.persona] {
				rec := getHost(h, seed)
				if rec.Code != http.StatusOK {
					t.Fatalf("%s: GET %s status = %d, want 200", tmpl, seed, rec.Code)
				}
				ct := rec.Header().Get("Content-Type")
				if !strings.Contains(ct, "html") && !strings.Contains(ct, "xml") {
					continue // JSON bodies carry no fetched references here
				}
				body := rec.Body.String()
				var refs []string
				for _, m := range attr.FindAllStringSubmatch(body, -1) {
					refs = append(refs, m[1])
				}
				for _, m := range loc.FindAllStringSubmatch(body, -1) {
					refs = append(refs, m[1])
				}
				for _, ref := range refs {
					if strings.HasPrefix(ref, "//") {
						t.Errorf("%s: %s emits protocol-relative %q — a hidden third-party fetch", tmpl, seed, ref)
						continue
					}
					if !strings.HasPrefix(ref, "/") {
						if !strings.HasPrefix(ref, "https://example.com") {
							continue // mailto:, external anchors, data:, javascript:
						}
						ref = strings.TrimPrefix(ref, "https://example.com")
					}
					ref = strings.SplitN(strings.SplitN(ref, "#", 2)[0], "&amp;", 2)[0]
					if ref == "" {
						ref = "/"
					}
					// The Sprintf-built pages escape % for the format verb; the
					// real bytes carry single % escapes.
					ref = strings.ReplaceAll(ref, "%%", "%")
					// Attribute-looking strings inside inline JS are not fetches;
					// anything that is not a sane absolute path is skipped.
					if strings.ContainsAny(ref, " \t\r\n\"'<>{}") {
						continue
					}
					if u, err := url.ParseRequestURI(ref); err != nil || u.Host != "" {
						continue
					}
					if r := getHost(h, ref); r.Code == http.StatusNotFound {
						t.Errorf("%s: %s references %q which 404s", tmpl, seed, ref)
					}
				}
			}
		})
	}
}

// 4. Engine signatures: status codes and headers taken from the real software.
// If these are wrong the persona is worse than a plain static site.
func TestEngineSignatures(t *testing.T) {
	t.Run("wordpress", func(t *testing.T) {
		h := newTestHandler(t, "10gag")
		if rec := getHost(h, "/"); !strings.Contains(rec.Body.String(), `<meta name="generator" content="WordPress `+h.ver) {
			t.Error("front page lacks the generator meta the persona claims")
		} else if rec.Header().Get("Link") == "" || rec.Header().Get("X-Pingback") == "" {
			t.Error("front page lacks the api.w.org Link / X-Pingback headers WP emits")
		}
		if rec := getHost(h, "/wp-admin"); rec.Code != http.StatusMovedPermanently || !strings.HasSuffix(rec.Header().Get("Location"), "/wp-admin/") {
			t.Errorf("/wp-admin = %d %q, want 301 to the slashed path", rec.Code, rec.Header().Get("Location"))
		}
		rec := getHost(h, "/wp-admin/")
		if rec.Code != http.StatusFound || !strings.Contains(rec.Header().Get("Location"), "/wp-login.php?redirect_to=") || !strings.Contains(rec.Header().Get("Location"), "reauth=1") {
			t.Errorf("/wp-admin/ = %d %q, want 302 to wp-login.php with redirect_to and reauth", rec.Code, rec.Header().Get("Location"))
		}
		if rec := getHost(h, "/xmlrpc.php"); rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != "POST" ||
			rec.Header().Get("Content-Type") != "text/plain" || rec.Body.String() != "XML-RPC server accepts POST requests only." {
			t.Errorf("xmlrpc.php GET = %d %q %q, want 405/Allow:POST/text/plain core message", rec.Code, rec.Header().Get("Allow"), rec.Body.String())
		}
		rec = httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "https://example.com/xmlrpc.php",
			strings.NewReader(`<methodCall><methodName>system.listMethods</methodName></methodCall>`)))
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "blogger.getUsersBlogs") {
			t.Errorf("xmlrpc listMethods = %d, want 200 with the method array", rec.Code)
		}
		if rec := getHost(h, "/wp-login.php"); !strings.Contains(rec.Header().Get("Set-Cookie"), "wordpress_test_cookie=WP%20Cookie%20check") {
			t.Error("login page missing the WP test cookie")
		}
		if rec := getHost(h, "/wp-json/"); rec.Code != http.StatusOK || !json.Valid(rec.Body.Bytes()) {
			t.Errorf("wp-json = %d, want 200 valid JSON", rec.Code)
		}
		// Version coherence: the readme must state the same version the meta tag does.
		if rec := getHost(h, "/readme.html"); !strings.Contains(rec.Body.String(), "<strong>"+h.ver+"</strong>") {
			t.Errorf("readme does not carry the persona version %s", h.ver)
		}
		if rec := getHost(h, "/wp-includes/version.php"); rec.Code != http.StatusForbidden {
			t.Errorf("blocked wp-includes file = %d, want 403", rec.Code)
		}
	})
	t.Run("joomla", func(t *testing.T) {
		h := newTestHandler(t, "YouTube")
		if rec := getHost(h, "/administrator"); rec.Code != http.StatusMovedPermanently || !strings.HasSuffix(rec.Header().Get("Location"), "/administrator/") {
			t.Errorf("/administrator = %d, want 301 to the slashed path", rec.Code)
		}
		if rec := getHost(h, "/administrator/"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `id="form-login"`) {
			t.Error("/administrator/ must render the Joomla login form")
		}
		if rec := getHost(h, "/cache/"); rec.Code != http.StatusForbidden {
			t.Errorf("/cache/ = %d, want 403", rec.Code)
		}
	})
	t.Run("drupal", func(t *testing.T) {
		h := newTestHandler(t, "speedtest")
		if rec := getHost(h, "/"); rec.Header().Get("X-Generator") != h.ver {
			t.Errorf("X-Generator = %q, want %q", rec.Header().Get("X-Generator"), h.ver)
		}
		if rec := getHost(h, "/user"); rec.Code != http.StatusFound || !strings.HasSuffix(rec.Header().Get("Location"), "/user/login") {
			t.Errorf("/user = %d, want 302 to /user/login", rec.Code)
		}
		if rec := getHost(h, "/core/"); rec.Code != http.StatusForbidden {
			t.Errorf("/core/ = %d, want 403", rec.Code)
		}
	})
	t.Run("bitrix", func(t *testing.T) {
		h := newTestHandler(t, "downloader")
		if rec := getHost(h, "/bitrix/"); rec.Code != http.StatusForbidden {
			t.Errorf("/bitrix/ = %d, want 403", rec.Code)
		}
		if rec := getHost(h, "/auth/"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `name="USER_LOGIN"`) {
			t.Error("/auth/ must render the Bitrix login form")
		}
		if rec := getHost(h, "/personal/"); rec.Code != http.StatusFound || !strings.Contains(rec.Header().Get("Location"), "/auth/?backurl=") {
			t.Errorf("/personal/ = %d, want 302 to auth with backurl", rec.Code)
		}
	})
	t.Run("dle", func(t *testing.T) {
		for _, tmpl := range []string{"converter", "tonari-no-totoro"} {
			h := newTestHandler(t, tmpl)
			if rec := getHost(h, "/"); !strings.Contains(rec.Body.String(), "DataLife Engine (v"+h.ver+")") {
				t.Errorf("%s: front page missing the DLE generator meta", tmpl)
			}
			if rec := getHost(h, "/admin.php"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "dle-login") {
				t.Errorf("%s: /admin.php must answer the DLE login form", tmpl)
			}
			if rec := getHost(h, "/engine/"); rec.Code != http.StatusForbidden {
				t.Errorf("%s: /engine/ = %d, want 403", tmpl, rec.Code)
			}
			if rec := getHost(h, "/statistics.html"); rec.Code != http.StatusOK {
				t.Errorf("%s: /statistics.html = %d, want 200", tmpl, rec.Code)
			}
			if rec := getHost(h, "/index.php?do=opensearch"); rec.Code != http.StatusOK ||
				!strings.Contains(rec.Header().Get("Content-Type"), "opensearchdescription") {
				t.Errorf("%s: opensearch not served", tmpl)
			}
		}
	})
	t.Run("nextcloud", func(t *testing.T) {
		h := newTestHandler(t, "nextcloud")
		rec := getHost(h, "/status.php")
		var st struct {
			Installed bool   `json:"installed"`
			Version   string `json:"version"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil || !st.Installed || st.Version != h.ver {
			t.Errorf("status.php = %d %s, want installed JSON at version %s", rec.Code, rec.Body.String(), h.ver)
		}
		if rec := getHost(h, "/remote.php/dav"); rec.Code != http.StatusUnauthorized || rec.Header().Get("WWW-Authenticate") == "" {
			t.Errorf("/remote.php = %d, want 401 with WWW-Authenticate", rec.Code)
		}
		if rec := getHost(h, "/login"); rec.Code != http.StatusSeeOther {
			t.Errorf("/login = %d, want 303 to the index login", rec.Code)
		}
		if rec := getHost(h, "/index.php/apps/settings"); rec.Code != http.StatusSeeOther {
			t.Errorf("app route = %d, want 303 to login (what Nextcloud answers unauthenticated)", rec.Code)
		}
		if strings.Contains(getHost(h, "/robots.txt").Body.String(), "Sitemap:") {
			t.Error("Nextcloud is an app, not a content site: its robots must not advertise a sitemap")
		}
	})
}

// 5. The manual's own probe: a guessed path on a CMS box must be a real 404 —
// status AND body (never the front page) — and Miss() must agree.
func TestCMSPersonasNotFoundLikeACMS(t *testing.T) {
	for tmpl, p := range personaTemplates {
		if p == pStatic {
			continue // the SPA fallback is pinned by its own tests
		}
		t.Run(tmpl, func(t *testing.T) {
			h := newTestHandler(t, tmpl)
			index := getHost(h, "/").Body.Bytes()
			rec := getHost(h, "/definitely-not-a-real-page")
			if rec.Code != http.StatusNotFound {
				t.Fatalf("guessed path = %d, want 404 — the notfound=200 tell", rec.Code)
			}
			if strings.TrimSpace(rec.Body.String()) == strings.TrimSpace(string(index)) {
				t.Error("404 status carrying the front page — the contradiction itself")
			}
			if !h.Miss("/definitely-not-a-real-page") {
				t.Error("handler 404s the path but Miss() says no — probe accounting disagrees")
			}
			// The extensionless guess too: CMSes do not fall back to an app shell.
			if rec := getHost(h, "/admin"); rec.Code != http.StatusNotFound && !h.personaOwns("admin") {
				if _, ok := h.files["admin"]; !ok {
					t.Errorf("/admin = %d, want 404 on a CMS", rec.Code)
				}
			}
		})
	}
}

// 6. Every response the persona layer produces declares its length — the same
// wire rule the static path follows — and nothing escapes as chunked.
func TestPersonaResponsesDeclareLength(t *testing.T) {
	for tmpl := range personaTemplates {
		h := newTestHandler(t, tmpl)
		srv := httptest.NewServer(h)
		for _, target := range append(personaSeeds[h.persona], "/xmlrpc.php", "/no-such-thing") {
			resp, err := srv.Client().Get(srv.URL + target)
			if err != nil {
				t.Fatalf("%s GET %s: %v", tmpl, target, err)
			}
			body := make([]byte, 0, 4096)
			buf := make([]byte, 1024)
			for {
				n, err := resp.Body.Read(buf)
				body = append(body, buf[:n]...)
				if err != nil {
					break
				}
			}
			resp.Body.Close()
			what := tmpl + " " + target
			if len(resp.TransferEncoding) > 0 {
				t.Errorf("%s: chunked (%v) — nothing on this box may chunk", what, resp.TransferEncoding)
			}
			if resp.ContentLength < 0 {
				t.Errorf("%s: no Content-Length", what)
			} else if int64(len(body)) != resp.ContentLength {
				t.Errorf("%s: Content-Length %d but %d bytes of body", what, resp.ContentLength, len(body))
			}
			if resp.Header.Get("Server") != ServerName {
				t.Errorf("%s: Server = %q", what, resp.Header.Get("Server"))
			}
		}
		srv.Close()
	}
}

// 7. The persona story is stable across restarts of one install: same version,
// same title, same bytes on the dynamic endpoints.
func TestPersonaStableAcrossRestarts(t *testing.T) {
	for tmpl := range personaTemplates {
		dir := t.TempDir()
		a := newTestHandler(t, tmpl) // fresh stamp per temp dir
		b, err := New(tmpl, LoadStamp(dir))
		if err != nil {
			t.Fatal(err)
		}
		c, err := New(tmpl, LoadStamp(dir))
		if err != nil {
			t.Fatal(err)
		}
		if b.ver != c.ver || b.persona != c.persona || b.siteName != c.siteName {
			t.Errorf("%s: persona drifted across a restart (%s vs %s)", tmpl, b.ver, c.ver)
		}
		if a.siteName == "" && a.persona == pWordPress {
			t.Errorf("%s: WordPress persona with an empty site name — feeds and REST would read broken", tmpl)
		}
	}
}

// 8. Two installs of the same persona must still differ: the version pool is
// keyed off the stamp, and the sitemap date rides the stamped index.
func TestPersonaVarietyAcrossInstalls(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 12; i++ {
		h := newTestHandler(t, "10gag")
		seen[h.ver] = true
	}
	if len(seen) < 2 {
		t.Errorf("12 installs of one persona all serve version %v — the fleet shares one fingerprint", seen)
	}
	// The version must always be a member of the pool, whatever the stamp says.
	h := newTestHandler(t, "10gag")
	found := false
	for _, v := range verPools[pWordPress] {
		if h.ver == v {
			found = true
		}
	}
	if !found {
		t.Errorf("version %q outside the WordPress pool", h.ver)
	}
}

func TestSitemapOnlyListsRealPages(t *testing.T) {
	for tmpl := range personaTemplates {
		h := newTestHandler(t, tmpl)
		if h.persona == pNextcloud {
			continue // no sitemap by design, asserted above
		}
		rec := getHost(h, "/sitemap.xml")
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: sitemap.xml = %d", tmpl, rec.Code)
		}
		loc := regexp.MustCompile(`<loc>([^<]+)</loc>`)
		n := 0
		for _, m := range loc.FindAllStringSubmatch(rec.Body.String(), -1) {
			p := strings.TrimPrefix(m[1], "https://example.com")
			if r := getHost(h, p); r.Code/100 != 2 {
				t.Errorf("%s: sitemap lists %q which answers %d — sitemap entries must be live", tmpl, p, r.Code)
			}
			n++
		}
		if n == 0 {
			t.Errorf("%s: sitemap.xml with no <loc> entries at all", tmpl)
		}
	}
}

func TestPOSTToPersonaEndpointsBehavesLikeTheEngine(t *testing.T) {
	// xmlrpc and the login forms belong to the engine, not to the blanket file-server
	// 405: the persona must take them before the method gate.
	h := newTestHandler(t, "10gag")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "https://example.com/xmlrpc.php", strings.NewReader("<methodCall><methodName>wp.getUsersBlogs</methodName></methodCall>")))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Incorrect username or password") {
		t.Errorf("wp.getUsersBlogs = %d %s, want a 200 fault", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "https://example.com/wp-login.php", strings.NewReader("log=x")))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Username</strong> is required") {
		t.Errorf("POST wp-login = %d, want the WP validation page", rec.Code)
	}
	// A method no engine implements on these paths still gets the file-server 405.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "https://example.com/wp-login.php", nil))
	if rec.Code != http.StatusMethodNotAllowed || rec.Body.Len() != 0 {
		t.Errorf("PUT wp-login = %d, want empty 405", rec.Code)
	}
}
