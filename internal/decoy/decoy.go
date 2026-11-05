// Package decoy serves an innocent-looking website ("decoy") for every
// request that doesn't carry the secret panel path. The goal: a visitor, DPI,
// or scanner sees an ordinary site, never a hint that a VPN panel exists.
//
// Looking ordinary is not only about the HTML. A probe compares what the server
// DOES against what it claims to be, so the handler mimics a static file server
// down to the mechanics: Content-Length on every response (net/http chunks bodies
// over its 2 KB sniff buffer, and no static host chunks static files),
// Last-Modified + ETag + conditional 304s, byte ranges, a 405 for methods a file
// server does not implement, and a not-found page whose status agrees with its
// body. See Handler.ServeHTTP.
package decoy

import (
	"bytes"
	"crypto/rand"
	"embed"
	"fmt"
	"io/fs"
	"math/big"
	"mime"
	"net"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"
)

//go:embed all:templates
var templatesFS embed.FS

// ServerName is the Server header the decoy presents. Exported because the port-80
// redirector has to present the same one — a host that answers 443 as Caddy and 80 as
// something else has told a fingerprinter more than either answer alone.
//
// It is deliberately NOT "nginx". Xray terminates TLS for :443 and falls back to
// this handler, so everything an outside prober fingerprints at the TLS layer —
// ClientHello response, extension order, session tickets, ALPN handling — is Go's
// crypto/tls. An nginx banner over a Go TLS stack is a contradiction that costs
// one JA4S lookup to spot. Caddy is Go, is a mainstream choice for exactly this
// kind of static site, and matches the behaviour implemented below, so the banner
// and the machine underneath tell the same story.
const ServerName = "Caddy"

// hostOf is the request's host without any port, for the robots.txt Host directive
// and the absolute URLs the persona emits.
func hostOf(r *http.Request) string {
	if h, _, err := net.SplitHostPort(r.Host); err == nil {
		return h
	}
	return r.Host
}

// Available returns the list of bundled template slugs.
func Available() ([]string, error) {
	entries, err := fs.ReadDir(templatesFS, "templates")
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out, nil
}

// busyTemplates are the slugs picked from at install time. A decoy is not only
// read by a scanner — it is also the cover story for the traffic volume the box
// carries, and gigabytes a day flowing to an 8 KB "coming soon" placeholder is a
// mismatch no amount of encryption hides. These templates are sites where large,
// long-lived transfers are the whole point.
//
// Left out are the templates that contradict a busy box rather than explain it:
// the placeholders (coming-soon), the maintenance pages (503-*, maintenance) and
// the bare nginx page. All stay selectable by hand — an operator who actually
// wants a site that looks parked can still say so.
var busyTemplates = []string{"filecloud", "downloader", "converter", "speedtest", "10gag", "YouTube", "nextcloud"}

// RandomTemplate returns a template slug for a fresh install. Choosing at random
// keeps a fleet from sharing one recognisable front page — the body stamp (see
// Stamp) separates two installs that land on the same slug, and this separates
// their look as well.
func RandomTemplate() string {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(busyTemplates))))
	if err != nil {
		return busyTemplates[0]
	}
	return busyTemplates[n.Int64()]
}

// asset is one preloaded file of a template, already stamped for this install.
// Preloading trades ~1 MB (the largest template) for a request path that neither
// allocates a copy of the file nor recomputes its validators.
type asset struct {
	name    string
	body    []byte
	ct      string
	etag    string
	modTime time.Time
}

// Handler serves a single decoy template.
type Handler struct {
	files    map[string]*asset
	index    *asset
	notFound *asset // the template's own 404.html, when it ships one
	down     bool   // maintenance template: every request answers 503 with the index page
	st       Stamp  // this install's entropy, kept so the persona can re-derive tokens
	// persona is the CMS this box presents as, tied to the template slug (see personaFor).
	// It drives robots.txt, the sitemap, the admin/feed/xmlrpc endpoints, the generator
	// tag and the not-found behaviour so the whole surface tells one story.
	persona  personaID
	ver      string // engine version, fixed per install from the stamp
	siteName string // the template's own page title, reused in feeds and metadata
	// favicon is the asset answered at /favicon.ico, the path browsers and probes
	// ask for regardless of what the templates' <link rel=icon> points at. Aliased
	// to the template's own icon so the root path never 404s.
	favicon *asset
}

// maintenanceTemplates are slugs whose front page advertises the site as
// temporarily unavailable; they should answer with 503, not 200, so the body and
// status agree (a "503" page served as 200 is an easy tell).
var maintenanceTemplates = map[string]bool{"503-1": true, "503-2": true, "maintenance": true}

// New returns a decoy handler for the given template slug, stamped with this
// install's entropy (see LoadStamp).
func New(template string, st Stamp) (*Handler, error) {
	if template == "" {
		template = "coming-soon"
	}
	sub, err := fs.Sub(templatesFS, path.Join("templates", template))
	if err != nil {
		return nil, fmt.Errorf("decoy template %q: %w", template, err)
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil, fmt.Errorf("decoy template %q missing index.html: %w", template, err)
	}

	h := &Handler{
		files:   map[string]*asset{},
		down:    maintenanceTemplates[template],
		st:      st,
		persona: personaFor(template),
	}
	h.ver = resolveVer(h.persona, st)
	if raw, err := fs.ReadFile(sub, "index.html"); err == nil {
		h.siteName = extractTitle(raw)
	}
	// The persona's head markers are injected into every page before the asset's
	// validators are derived, so the ETag and Content-Length describe the bytes that
	// actually go out.
	head := h.headSnippet()

	err = fs.WalkDir(sub, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		body, err := fs.ReadFile(sub, name)
		if err != nil {
			return err
		}
		if head != "" && strings.HasSuffix(name, ".html") {
			body = injectHead(body, head)
		}
		h.files[name] = newAsset(name, body, st)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("decoy template %q: %w", template, err)
	}
	h.index = h.files["index.html"]
	h.notFound = h.files["404.html"]
	h.favicon = pickFavicon(h.files)
	if h.favicon == nil {
		// A template that ships no icon file at all (nextcloud) still must answer
		// /favicon.ico: a transparent 1x1 ICO, the smallest body a probe accepts.
		h.favicon = newAsset("favicon.ico", minimalICO, st)
	}
	return h, nil
}

// minimalICO is a 1x1 transparent 32bpp .ico: ICONDIR + one ICONDIRENTRY + a
// BITMAPINFOHEADER + one XOR pixel + the AND mask row, 70 bytes.
var minimalICO = []byte{
	0x00, 0x00, 0x01, 0x00, 0x01, 0x00,
	0x01, 0x01, 0x00, 0x00, 0x01, 0x00, 0x20, 0x00, 0x30, 0x00, 0x00, 0x00, 0x16, 0x00, 0x00, 0x00,
	0x28, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x02, 0x00, 0x00, 0x00, 0x01, 0x00, 0x20, 0x00,
	0x00, 0x00, 0x00, 0x00, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	0x00, 0x00, 0x00, 0x00,
	0x00, 0x00, 0x00, 0x00,
}

// pickFavicon chooses the asset answered at /favicon.ico: the template's own icon,
// preferring a real .ico at the root, then any .ico, then a favicon png/svg. A site
// whose icon only lives under a deep path still answers the root path browsers and
// probes request first.
func pickFavicon(files map[string]*asset) *asset {
	for _, want := range []string{"favicon.ico"} {
		if a, ok := files[want]; ok {
			return a
		}
	}
	var ico, png *asset
	for name, a := range files {
		switch {
		case strings.HasSuffix(name, "/favicon.ico") || strings.HasSuffix(name, ".ico"):
			if ico == nil {
				ico = a
			}
		case strings.Contains(name, "favicon") && (strings.HasSuffix(name, ".png") || strings.HasSuffix(name, ".svg")):
			if png == nil {
				png = a
			}
		}
	}
	if ico != nil {
		return ico
	}
	return png
}

// newAsset stamps one file and derives its validators. Only HTML carries the
// per-install mark: binary assets have no place to put one, and their bytes are
// not what a body-hash search matches on anyway.
func newAsset(name string, body []byte, st Stamp) *asset {
	if strings.HasSuffix(name, ".html") {
		body = stampHTML(body, st.mark())
	}
	mod := st.modTime(name)
	return &asset{
		name:    name,
		body:    body,
		ct:      contentType(name),
		modTime: mod,
		// Caddy's static ETag: base-36 modification time followed by the hex size.
		etag: fmt.Sprintf("%q", strconv.FormatInt(mod.Unix(), 36)+strconv.FormatInt(int64(len(body)), 16)),
	}
}

// stampHTML inserts the per-install mark just before </body>, or appends it when
// the document has no body tag.
func stampHTML(body []byte, mark string) []byte {
	at := bytes.LastIndex(bytes.ToLower(body), []byte("</body>"))
	if at < 0 {
		return append(append(append([]byte{}, body...), '\n'), mark...)
	}
	out := make([]byte, 0, len(body)+len(mark)+1)
	out = append(out, body[:at]...)
	out = append(out, mark...)
	out = append(out, '\n')
	return append(out, body[at:]...)
}

// ServeHTTP answers like a static file server: known paths are served with full
// validators, unknown ones get the template's not-found behaviour, and methods a
// file server doesn't implement get a 405.
//
// It never carries Alt-Svc: see the package's h3.go for why advertising HTTP/3 here
// would lock a browser out of the panel.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Server", ServerName)
	// Ordinary HTTPS sites carry HSTS; its absence is one of the block-3 tells the
	// field probe checks. The decoy is only ever reached over TLS, so setting it
	// unconditionally is correct and costs nothing on the plain-HTTP path (which
	// never reaches this handler).
	w.Header().Set("Strict-Transport-Security", "max-age=31536000")

	// A maintenance decoy is down for everything, method included. The config it
	// imitates (nginx `return 503` for the whole server) answers every request the
	// same way, so gating methods first would have it refuse a POST with 405 while
	// its own page says the site is unavailable.
	if h.down {
		h.serve(w, r, h.index, http.StatusServiceUnavailable)
		return
	}

	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "" {
		name = "index.html"
	}

	// The persona owns its CMS surface outright — the admin endpoints, feeds,
	// sitemap, xmlrpc, directory blocks — and decides its own method handling (a POST
	// belongs to wp-login/xmlrpc/wp-cron, not a blanket 405). It runs before the
	// file-server method gate below so those dynamic routes stay reachable.
	if h.personaServe(w, r, name) {
		return
	}

	// Headers the persona leaks onto the pages it does not serve itself — most
	// importantly the static front page, which has to carry the same generator and
	// api.w.org / X-Generator signals as the rest of the surface it advertises.
	h.extraHeaders(w, r, name)

	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		// A static host has nothing to POST to, and OPTIONS isn't something a file
		// server implements either — nginx and Caddy both answer 405. Serving the full
		// front page under a 200 for every method, as this used to, is not something
		// either does.
		w.Header().Set("Allow", "GET, HEAD")
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// robots.txt: a template that bundles its own wins; otherwise synthesize one
	// naming the host it was asked for, so the path never 404s and it never promises
	// a link the persona does not answer.
	if name == "robots.txt" {
		a, ok := h.files[name]
		if !ok {
			a = h.robotsAsset(hostOf(r))
		}
		h.serve(w, r, a, http.StatusOK)
		return
	}
	// /favicon.ico: browsers and probes ask the root path even when the template's
	// <link rel=icon> points deeper; alias the template's own icon so it never 404s.
	if name == "favicon.ico" {
		a, ok := h.files[name]
		if !ok {
			a = h.favicon
		}
		if a != nil {
			h.serve(w, r, a, http.StatusOK)
			return
		}
	}
	a, ok := h.files[name]
	if !ok {
		h.serveMiss(w, r, name)
		return
	}
	h.serve(w, r, a, http.StatusOK)
}

// robotsAsset builds the synthesized robots.txt for one host, with the same
// validators a shipped asset gets so conditional requests and ranges behave.
func (h *Handler) robotsAsset(host string) *asset {
	body := []byte(h.robotsText(host))
	mod := h.index.modTime
	return &asset{
		name:    "robots.txt",
		body:    body,
		ct:      "text/plain; charset=utf-8",
		modTime: mod,
		etag:    fmt.Sprintf("%q", strconv.FormatInt(mod.Unix(), 36)+strconv.FormatInt(int64(len(body)), 16)),
	}
}

// Miss reports whether urlPath would be served as a genuine "not found" (a 404) rather
// than a real page — mirroring what serveMiss actually does, so probe detection counts
// exactly the requests the decoy treats as misses. A scanner guessing the hidden panel
// path hits misses; a browser loading the decoy's own pages and assets does not.
//
// The subtlety is the single-page templates: they ship no 404 page and answer an
// EXTENSIONLESS miss with the index under 200 (the `try_files $uri /index.html` case),
// so that is NOT a miss — only a template with its own 404 page, or a missing asset
// (a path with an extension), is. Counting extensionless SPA fallbacks would flag a
// visitor following an internal client-route link as a scanner.
func (h *Handler) Miss(urlPath string) bool {
	name := strings.TrimPrefix(path.Clean("/"+urlPath), "/")
	if name == "" {
		name = "index.html"
	}
	if _, ok := h.files[name]; ok {
		return false // a real page or asset the template ships
	}
	if name == "robots.txt" {
		return false // synthesized on the fly, never a 404
	}
	if name == "favicon.ico" && h.favicon != nil {
		return false // aliased to the template's icon, never a 404
	}
	if h.personaOwns(name) {
		return false // the persona answers it: sitemap, admin endpoints, feeds, dir blocks
	}
	// A CMS box 404s an unknown path — including an extensionless one — exactly as a
	// real WordPress/Drupal site does; that is the manual's own `notfound=404` check.
	// A classic template with its own 404 page 404s too. Only a genuine single-page
	// app keeps the `try_files /index.html` fallback, and there only an asset miss is.
	if h.notFound != nil || h.isCMS() {
		return true
	}
	return path.Ext(name) != ""
}

// serveMiss answers a path the template doesn't have.
func (h *Handler) serveMiss(w http.ResponseWriter, r *http.Request, name string) {
	switch {
	// A template with its own 404 page is a classic static site: every miss is a 404
	// carrying that page.
	case h.notFound != nil:
		h.serve(w, r, h.notFound, http.StatusNotFound)

	// A CMS box does not fall back to its index for a guessed path the way an SPA
	// does — it answers 404, with its own not-found page. Serving the front page
	// under 200 here would contradict the engine the rest of the surface advertises
	// and trip the manual's `notfound=404` check.
	case h.isCMS():
		h.persona404(w, r)

	// The single-page templates ship no 404 page, and the hosting they imitate
	// (`try_files $uri /index.html`) answers an extensionless miss with the app
	// shell under a 200. Serving that same shell under a 404 — which is what
	// falling back to index used to do — is a contradiction no static host
	// produces, and one GET of a random path next to a GET of / exposes it.
	case path.Ext(name) == "":
		h.serve(w, r, h.index, http.StatusOK)

	// A missing asset is a genuine 404, with the empty body such hosts return.
	default:
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusNotFound)
	}
}

// serve writes one asset. The 200 path goes through http.ServeContent, which
// supplies Content-Length, Last-Modified, Accept-Ranges, byte ranges and the
// conditional 304s — the behaviour a static server has and a plain Write does
// not. Other statuses are written directly (ServeContent always writes 200), with
// Content-Length set so those bodies aren't chunked either.
func (h *Handler) serve(w http.ResponseWriter, r *http.Request, a *asset, status int) {
	w.Header().Set("Content-Type", a.ct)
	w.Header().Set("Etag", a.etag)
	if status == http.StatusOK {
		http.ServeContent(w, r, a.name, a.modTime, bytes.NewReader(a.body))
		return
	}
	w.Header().Set("Last-Modified", a.modTime.UTC().Format(http.TimeFormat))
	w.Header().Set("Content-Length", strconv.Itoa(len(a.body)))
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = w.Write(a.body)
	}
}

func contentType(name string) string {
	if ct := mime.TypeByExtension(path.Ext(name)); ct != "" {
		return ct
	}
	return "text/html; charset=utf-8"
}
