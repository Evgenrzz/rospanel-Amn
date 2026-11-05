package decoy

// Every covered box also has to look like a website someone actually maintains,
// not a one-file placeholder. A probe does not fingerprint the cryptography — it
// reads the cheap things: the front page, a 404 on a guessed path, robots.txt, a
// favicon, and then it follows the promises the site makes. A robots.txt that
// disallows /wp-admin/ and advertises a sitemap.xml is a promise; if /wp-admin/
// 404s and sitemap.xml is missing, the file has said "WordPress" while the box
// says "nothing", and that contradiction is worth more to a scanner than any
// banner. So the decoy commits to a persona: one CMS per template, and the whole
// surface — robots.txt, sitemap, feeds, the admin endpoints, the generator tag,
// even the 404 page — agrees with it.
//
// The persona is tied to the template rather than rolled at random, so the claim
// matches the look: a filecloud front page is never served under a WordPress
// robots.txt, an anime portal that already posts to /index.php?do= is a DataLife
// Engine site, and the Nextcloud login page behaves like Nextcloud. Two installs
// still differ (the stamp moves the version string, the site title, the dates and
// every body hash), they just differ *coherently*.
//
// Status codes and content types are taken from the real engines, not invented:
// xmlrpc.php on GET is 405 + Allow: POST + text/plain, /wp-admin/ is a 302 to
// wp-login.php?redirect_to=…&reauth=1, feeds are text/xml, a blocked directory is
// a 403. Getting these wrong is worse than not answering at all, because a
// fingerprinter's whole job is noticing a shape that is almost-but-not-quite right.

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type personaID int

const (
	pStatic personaID = iota // an honest single-page app (filecloud, the nginx page)
	pWordPress
	pJoomla
	pDrupal
	pBitrix
	pDLE
	pNextcloud
)

// personaFor is the CMS a template presents as. The choice follows the template's
// own markup and language so the persona is corroborated by what the page already
// contains, not merely asserted by robots.txt.
func personaFor(template string) personaID {
	switch template {
	case "10gag":
		return pWordPress
	case "YouTube":
		return pJoomla
	case "speedtest":
		return pDrupal
	case "downloader":
		return pBitrix
	case "converter", "tonari-no-totoro":
		return pDLE
	case "nextcloud":
		return pNextcloud
	default:
		return pStatic
	}
}

// isCMS reports whether the persona is a content management system that would
// answer an unknown path with a 404 rather than the app shell. Static boxes
// imitate `try_files $uri /index.html` hosting (a client-routing SPA), which is a
// different, also-ordinary shape; a CMS is not.
func (h *Handler) isCMS() bool { return h.persona != pStatic }

// verPools lists the versions each engine is plausibly found on, newest first.
// Kept as a package var so tests can assert a stamp-picked version is a member.
var verPools = map[personaID][]string{
	pWordPress: {"6.7.2", "6.6.3", "6.5.6", "6.4.4", "6.3.5"},
	pJoomla:    {"5.2.4", "5.1.5", "4.4.12", "3.10.12"},
	pDrupal:    {"Drupal 10", "Drupal 10.2", "Drupal 11", "Drupal 9.5"},
	pBitrix:    {"24.0.400", "22.5.100", "24.1.200"},
	pDLE:       {"24.4", "24.1", "23.2", "22.4"},
	pNextcloud: {"30.0.4.1", "29.0.9.2", "28.0.14.1", "31.0.1.2"},
}

// resolveVer fixes this install's engine version once, from the stamp, so the
// generator tag, the readme, the feed metadata and every ?ver= query string agree
// and stay put across restarts. A site that announces a different version on two
// pages is a tell in itself.
func resolveVer(p personaID, st Stamp) string {
	pool, ok := verPools[p]
	if !ok || len(pool) == 0 {
		return ""
	}
	return pool[st.u64("engine-ver")%uint64(len(pool))]
}

// extractTitle is the template's own page title, reused as the site name so the
// feed channel, the REST index, the readme and the sitemap all speak with the same
// name the front page carries.
func extractTitle(body []byte) string {
	low := strings.ToLower(string(body))
	i := strings.Index(low, "<title")
	if i < 0 {
		return ""
	}
	gt := strings.IndexByte(string(body[i:]), '>')
	if gt < 0 {
		return ""
	}
	start := i + gt + 1
	end := strings.Index(strings.ToLower(string(body[start:])), "</title>")
	if end < 0 {
		return ""
	}
	t := strings.Join(strings.Fields(string(body[start:start+end])), " ")
	if len(t) > 90 {
		t = t[:90]
	}
	return strings.TrimSpace(t)
}

// home is the absolute base URL for this request — the engine emits absolute URLs
// in feeds, sitemaps and forms, and they have to name the host it was asked on.
func (h *Handler) home(r *http.Request) string { return "https://" + hostOf(r) }

// xmlEsc escapes text for an XML document.
var xmlRepl = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")

func xmlEsc(s string) string { return xmlRepl.Replace(s) }

// headSnippet is injected into every HTML page the persona serves so the front
// page itself advertises the engine the rest of the surface implies. The links are
// relative — every target resolves through personaServe — so no page depends on
// knowing the host at build time and nothing loads cross-origin.
func (h *Handler) headSnippet() string {
	switch h.persona {
	case pWordPress:
		return "<meta name=\"generator\" content=\"WordPress " + h.ver + "\" />\n" +
			"<link rel=\"alternate\" type=\"application/rss+xml\" title=\"" + xmlEsc(h.siteName) + " &laquo; Feed\" href=\"/feed/\" />\n" +
			"<link rel=\"alternate\" type=\"application/rss+xml\" title=\"" + xmlEsc(h.siteName) + " &laquo; Comments Feed\" href=\"/comments/feed/\" />\n" +
			"<link rel=\"EditURI\" type=\"application/rsd+xml\" title=\"RSD\" href=\"/xmlrpc.php?rsd\" />\n" +
			"<link rel=\"wlwmanifest\" type=\"application/wlwmanifest+xml\" href=\"/wp-includes/wlwmanifest.xml\" />\n" +
			"<link rel=\"https://api.w.org/\" href=\"/wp-json/\" />\n" +
			"<link rel=\"shortlink\" href=\"/\" />"
	case pJoomla:
		return "<meta name=\"generator\" content=\"Joomla! - Open Source Content Management\" />"
	case pDrupal:
		return "<meta name=\"Generator\" content=\"" + xmlEsc(h.ver) + " (https://www.drupal.org)\" />"
	case pDLE:
		return "<meta name=\"generator\" content=\"DataLife Engine (v" + h.ver + ")\" />\n" +
			"<link rel=\"alternate\" type=\"application/rss+xml\" title=\"RSS\" href=\"/index.php?do=rss\" />"
	default:
		return ""
	}
}

// injectHead places snippet just before </head>, falling back to appending when a
// document has no head element.
func injectHead(body []byte, snippet string) []byte {
	if snippet == "" {
		return body
	}
	at := strings.LastIndex(strings.ToLower(string(body)), "</head>")
	if at < 0 {
		return body
	}
	out := make([]byte, 0, len(body)+len(snippet)+1)
	out = append(out, body[:at]...)
	out = append(out, snippet...)
	out = append(out, '\n')
	return append(out, body[at:]...)
}

// robotsText is this persona's robots.txt for the given host. It carries the
// Yandex Host directive and, where the persona is a content site, a Sitemap line —
// and every path it names is answered by personaServe, so the file never promises
// a dead link. Nextcloud is an app, not a content site, so it advertises no
// sitemap (and none is served for it).
func (h *Handler) robotsText(host string) string {
	sitemap := "\nSitemap: https://" + host + "/sitemap.xml"
	switch h.persona {
	case pWordPress:
		return "User-agent: *\n" +
			"Disallow: /wp-admin/\n" +
			"Allow: /wp-admin/admin-ajax.php\n" +
			"Disallow: /wp-login.php\n" + sitemap + "\n" +
			"Host: " + host + "\n"
	case pDLE:
		return "User-agent: *\n" +
			"Disallow: /admin.php\n" +
			"Disallow: /engine/\n" +
			"Disallow: /statistics.html\n" +
			"Disallow: /index.php?do=\n" +
			"Disallow: /*?newsid=" + sitemap + "\n" +
			"Host: " + host + "\n"
	case pJoomla:
		return "User-agent: *\n" +
			"Disallow: /administrator/\n" +
			"Disallow: /cache/\n" +
			"Disallow: /components/\n" +
			"Disallow: /modules/\n" +
			"Disallow: /plugins/\n" +
			"Disallow: /index.php?option=" + sitemap + "\n" +
			"Host: " + host + "\n"
	case pDrupal:
		return "User-agent: *\n" +
			"Disallow: /core/\n" +
			"Disallow: /modules/\n" +
			"Disallow: /themes/\n" +
			"Disallow: /profiles/\n" +
			"Disallow: /user/login\n" +
			"Disallow: /search/node" + sitemap + "\n" +
			"Host: " + host + "\n"
	case pBitrix:
		return "User-agent: *\n" +
			"Disallow: /bitrix/\n" +
			"Disallow: /upload/\n" +
			"Disallow: /auth/\n" +
			"Disallow: /personal/\n" +
			"Disallow: /*?bx=" + sitemap + "\n" +
			"Host: " + host + "\n"
	case pNextcloud:
		return "User-agent: *\n" +
			"Disallow: /remote.php\n" +
			"Disallow: /index.php/apps/\n" +
			"Disallow: /status.php\n" +
			"Host: " + host + "\n"
	default: // static
		return "User-agent: *\n" +
			"Disallow: /admin\n" +
			"Disallow: /cgi-bin" + sitemap + "\n" +
			"Host: " + host + "\n"
	}
}

// dyn writes a dynamically-generated response: full validators are deliberately
// absent (PHP-rendered pages don't carry a static-file ETag), but a Content-Length
// always is, so nothing on this box ever chunks.
func (h *Handler) dyn(w http.ResponseWriter, r *http.Request, status int, ctype, body string, hdr map[string]string) {
	for k, v := range hdr {
		w.Header().Set(k, v)
	}
	if ctype != "" {
		w.Header().Set("Content-Type", ctype)
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	if r.Method != http.MethodHead && body != "" {
		_, _ = io.WriteString(w, body)
	}
}

// redirect answers a Location hop with no body — the shape wp_redirect and a
// framework's own redirect emit.
func (h *Handler) redirect(w http.ResponseWriter, r *http.Request, code int, loc string) {
	w.Header().Set("Location", loc)
	w.Header().Set("Content-Length", "0")
	w.WriteHeader(code)
}

const (
	ctHTML = "text/html; charset=UTF-8"
	ctXML  = "text/xml; charset=UTF-8"
	ctJSON = "application/json; charset=UTF-8"
	ctCSS  = "text/css; charset=UTF-8"
	ctJS   = "application/javascript; charset=UTF-8"
)

// extraHeaders are the response headers a persona leaks on pages it does not own
// outright (most importantly the static front page), so the homepage itself tells
// the same story as the admin endpoints.
func (h *Handler) extraHeaders(w http.ResponseWriter, r *http.Request, name string) {
	switch h.persona {
	case pWordPress:
		if name == "index.html" {
			u := h.home(r)
			w.Header().Set("Link", "<"+u+"/wp-json/>; rel=\"https://api.w.org/\"")
			w.Header().Set("X-Pingback", u+"/xmlrpc.php")
		}
	case pDrupal:
		w.Header().Set("X-Generator", h.ver)
	case pNextcloud:
		h.drupalNoop()
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.Header().Set("X-Robots-Tag", "noindex, nofollow")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Permitted-Cross-Domain-Policies", "none")
	}
}

// drupalNoop keeps the nextcloud branch free of a stray symbol reference; the
// header set is what matters, not the (empty) helper.
func (h *Handler) drupalNoop() {}

// personaServe dispatches one cleaned request name to the persona's router. It
// returns true when it answered (including 403/405/redirects it owns); false lets
// ServeHTTP fall through to the static template and the shared robots/favicon
// handling.
func (h *Handler) personaServe(w http.ResponseWriter, r *http.Request, name string) bool {
	switch h.persona {
	case pWordPress:
		return h.wpServe(w, r, name)
	case pJoomla:
		return h.joomlaServe(w, r, name)
	case pDrupal:
		return h.drupalServe(w, r, name)
	case pBitrix:
		return h.bitrixServe(w, r, name)
	case pDLE:
		return h.dleServe(w, r, name)
	case pNextcloud:
		return h.nextcloudServe(w, r, name)
	default:
		return h.staticServe(w, r, name)
	}
}

// personaOwns reports whether a path is answered by the persona rather than
// treated as a not-found, so Miss() keeps counting exactly what the handler 404s.
func (h *Handler) personaOwns(name string) bool {
	switch name {
	case "sitemap.xml", "index.php":
		return h.persona != pNextcloud || name == "index.php"
	}
	switch h.persona {
	case pWordPress:
		return wpOwns(name)
	case pJoomla:
		return name == "administrator" || strings.HasPrefix(name, "administrator/") || name == "cache" || name == "components" || name == "modules" || name == "plugins"
	case pDrupal:
		return strings.HasPrefix(name, "core/") || strings.HasPrefix(name, "user") || name == "rss.xml" || strings.HasPrefix(name, "modules/") || strings.HasPrefix(name, "themes/") || strings.HasPrefix(name, "profiles/") ||
			name == "search" || strings.HasPrefix(name, "search/")
	case pBitrix:
		return name == "bitrix" || strings.HasPrefix(name, "bitrix/") || name == "upload" || strings.HasPrefix(name, "upload/") || name == "auth" || strings.HasPrefix(name, "auth/") || name == "personal" || strings.HasPrefix(name, "personal/")
	case pDLE:
		return name == "admin.php" || name == "login.php" || name == "engine" || strings.HasPrefix(name, "engine/") ||
			name == "statistics.html" || name == "statistics" || strings.HasPrefix(name, "statistics/") ||
			name == "rss.xml" || name == "last_rss" || name == "info_rss" || strings.HasPrefix(name, "index.php")
	case pNextcloud:
		return nextcloudOwns(name)
	}
	return false
}

func wpOwns(name string) bool {
	switch {
	case name == "readme.html", name == "xmlrpc.php", name == "wp-login.php", name == "wp-cron.php",
		name == "wp-login.js", name == "wp-json", name == "wp-admin", name == "wp-includes",
		name == "feed", name == "comments/feed", name == "wp-includes/wlwmanifest.xml":
		return true
	}
	return strings.HasPrefix(name, "wp-admin/") ||
		strings.HasPrefix(name, "wp-includes/") ||
		strings.HasPrefix(name, "wp-json/") ||
		strings.HasPrefix(name, "wp-content/") ||
		strings.HasPrefix(name, "feed/")
}

// ---- static persona: an honest SPA box. Only /index.php and /sitemap.xml.

func (h *Handler) staticServe(w http.ResponseWriter, r *http.Request, name string) bool {
	switch name {
	case "index.php":
		h.serve(w, r, h.index, http.StatusOK)
		return true
	case "sitemap.xml":
		h.serveSitemap(w, r)
		return true
	}
	return false
}

// ---- WordPress.

func (h *Handler) wpServe(w http.ResponseWriter, r *http.Request, name string) bool {
	read := r.Method == http.MethodGet || r.Method == http.MethodHead
	switch {
	case name == "index.php":
		if !read {
			return false
		}
		h.serve(w, r, h.index, http.StatusOK)
		return true

	case name == "sitemap.xml":
		h.serveSitemap(w, r)
		return true

	case name == "feed" || strings.HasPrefix(name, "feed/") || name == "comments/feed" || name == "index.php/feed":
		if !read {
			h.methodNotAllowed(w)
			return true
		}
		h.serveRSS(w, r, strings.Contains(name, "atom"), strings.Contains(name, "comments"))
		return true

	case name == "xmlrpc.php":
		h.xmlrpc(w, r)
		return true

	case name == "wp-login.php":
		h.wpLogin(w, r)
		return true

	case name == "wp-admin":
		// path.Clean drops the trailing slash, so /wp-admin and /wp-admin/ arrive
		// under one name; WordPress answers them differently and the difference is
		// exactly what a probe checks: the bare path gains the slash (301), the
		// slashed path bounces an anonymous visitor to the login (302). Redirecting
		// both to the slashed path is an infinite loop.
		if strings.HasSuffix(r.URL.Path, "/") {
			h.redirect(w, r, http.StatusFound, h.home(r)+"/wp-login.php?redirect_to="+urlEncode(h.home(r)+"/wp-admin/")+"&reauth=1")
			return true
		}
		h.redirect(w, r, http.StatusMovedPermanently, h.home(r)+"/wp-admin/")
		return true
	case name == "wp-admin/load-styles.php":
		h.dyn(w, r, http.StatusOK, ctCSS, "/* wp-admin css */\nhtml{background:#f1f1f1}\n", nil)
		return true
	case name == "wp-admin/admin-ajax.php":
		// robots.txt explicitly Allows this path, so it must answer like WP does to a
		// bare call: a 400 carrying a lone "0" from the dying ajax handler.
		if r.URL.Query().Get("action") == "" {
			h.dyn(w, r, http.StatusBadRequest, ctHTML, "0", nil)
			return true
		}
		h.dyn(w, r, http.StatusForbidden, ctHTML, "-1", nil)
		return true
	case strings.HasPrefix(name, "wp-admin/"):
		q := urlEncode(h.home(r) + r.URL.Path)
		h.redirect(w, r, http.StatusFound, h.home(r)+"/wp-login.php?redirect_to="+q+"&reauth=1")
		return true

	case name == "wp-includes/wlwmanifest.xml":
		h.serveWlwmanifest(w, r)
		return true
	case name == "wp-includes/js/wp-polyfill.min.js":
		h.dyn(w, r, http.StatusOK, ctJS, "/* wp-polyfill "+h.ver+" */\n", nil)
		return true
	case name == "wp-includes/images/blank.gif":
		// The wlwmanifest logo attribute points here; WordPress ships the file, it is
		// not one of the blocked includes scripts.
		h.dyn(w, r, http.StatusOK, "image/gif", string(onePixelGIF), nil)
		return true
	case strings.HasPrefix(name, "wp-includes/"):
		h.blockDir(w, r)
		return true

	case name == "wp-login.js":
		h.dyn(w, r, http.StatusOK, ctJS, "/* wp-login */\n", nil)
		return true
	case name == "wp-cron.php":
		h.dyn(w, r, http.StatusOK, ctHTML, "", map[string]string{"X-Content-Type-Options": "nosniff"})
		return true

	case name == "readme.html":
		h.wpReadme(w, r)
		return true

	case name == "wp-json" || strings.HasPrefix(name, "wp-json/"):
		h.wpREST(w, r, name)
		return true

	case name == "robots.txt", name == "favicon.ico":
		return false
	}
	return false
}

func (h *Handler) wpLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodDelete || r.Method == http.MethodPut {
		h.methodNotAllowed(w)
		return
	}
	switch r.URL.Query().Get("action") {
	case "lostpassword":
		h.wpLostPassword(w, r)
		return
	case "register", "rp":
		// Registration off is the stock WordPress setting, and the stock page says so.
		h.dyn(w, r, http.StatusOK, ctHTML, `<!DOCTYPE html>
<html lang="en-US">
<head><meta charset="UTF-8" /><title>Registration &rsaquo; `+xmlEsc(h.siteName)+` WordPress</title></head>
<body class="login wp-core-ui locale-en-us"><div class="login">
<h1><a href="`+h.home(r)+`">`+xmlEsc(h.siteName)+`</a></h1>
<p>User registration is currently not allowed.</p>
<p class="submit"><a class="button" href="`+h.home(r)+`/wp-login.php">Log In</a></p>
</div></body>
</html>
`, map[string]string{"Cache-Control": "no-store, no-cache, must-revalidate, max-age=0"})
		return
	}
	u := h.home(r)
	errBlock := ""
	if r.Method == http.MethodPost {
		errBlock = "\t\t\t<p class=\"message\">ERROR: <strong>Username</strong> is required.</p>"
	}
	body := fmt.Sprintf(`<!DOCTYPE html>
<html lang="en-US">
<head>
<meta http-equiv="Content-Type" content="text/html; charset=UTF-8" />
<meta name="viewport" content="width=device-width" />
<meta name='robots' content='noindex, follow' />
<title>Log In &rsaquo; %s WordPress</title>
<link rel='stylesheet' href='%s/wp-admin/load-styles.php?c=0&dir=ltr&load%%5Bchunk_0%%5D=dashicons,buttons,forms,l10n,login&ver=%s' />
<script src='%s/wp-includes/js/wp-polyfill.min.js?ver=7.4.4' id='wp-polyfill-js'></script>
</head>
<body class="login wp-core-ui locale-en-us">
<div class="login">
<h1><a href="%s">%s</a></h1>
%s
<form name="loginform" id="loginform" action="%s/wp-login.php" method="post">
	<p><label for="user_login">Username or Email Address</label><br /><input type="text" name="log" id="user_login" class="input" value="" size="20" autocapitalize="off" /></p>
	<p><label for="user_pass">Password</label><br /><input type="password" name="pwd" id="user_pass" class="input password-text" value="" size="20" /><button type="button" class="button wp-hide-pw dashicons dashicons-visibility" aria-label="Show password"></button></p>
	<label><input name="rememberme" type="checkbox" id="rememberme" value="forever" /> Remember Me</label>
	<p class="submit"><input type="submit" name="wp-submit" id="wp-submit" class="button button-primary button-large" value="Log In" /><input type="hidden" name="redirect_to" value="%s/wp-admin/" /><input type="hidden" name="testcookie" value="1" /></p>
</form>
<p id="nav"><a href="%s/wp-login.php?action=lostpassword">Lost your password?</a></p>
<script>document.cookie='wordpress_test_cookie=WP%%20Cookie%%20check; path=/';</script>
<p id="backtoblog"><a href="%s/">&larr; Go to %s</a></p>
</div>
<script src='%s/wp-login.js?ver=%s' id='login-js'></script>
</body>
</html>
`, xmlEsc(h.siteName), u, h.ver, u, u, xmlEsc(h.siteName), errBlock, u, u, u, u, xmlEsc(h.siteName), u, h.ver)
	h.dyn(w, r, http.StatusOK, ctHTML, body, map[string]string{
		"Set-Cookie":             "wordpress_test_cookie=WP%20Cookie%20check; path=/",
		"Cache-Control":          "no-store, no-cache, must-revalidate, max-age=0",
		"X-Content-Type-Options": "nosniff",
		"Pragma":                 "no-cache",
	})
}

// wpLostPassword is the reset form the login page links to; WordPress renders it
// with the same stylesheet bundle, so the reference stays alive.
func (h *Handler) wpLostPassword(w http.ResponseWriter, r *http.Request) {
	u := h.home(r)
	body := `<!DOCTYPE html>
<html lang="en-US">
<head>
<meta http-equiv="Content-Type" content="text/html; charset=UTF-8" />
<meta name="viewport" content="width=device-width" />
<meta name='robots' content='noindex, follow' />
<title>Lost Password &rsaquo; ` + xmlEsc(h.siteName) + ` WordPress</title>
<link rel='stylesheet' href='` + u + `/wp-admin/load-styles.php?c=0&dir=ltr&load%5Bchunk_0%5D=dashicons,buttons,forms,l10n,login&ver=` + h.ver + `' />
</head>
<body class="login wp-core-ui locale-en-us">
<div class="login">
<h1><a href="` + u + `">` + xmlEsc(h.siteName) + `</a></h1>
<form name="lostpasswordform" id="lostpasswordform" action="` + u + `/wp-login.php?action=lostpassword" method="post">
	<p><label for="user_login">Username or Email Address</label><br /><input type="text" name="user_login" id="user_login" class="input" value="" size="20" autocapitalize="off" /></p>
	<p class="submit"><input type="submit" name="wp-submit" id="wp-submit" class="button button-primary button-large" value="Get New Password" /></p>
</form>
<p id="nav"><a href="` + u + `/wp-login.php?action=register">Register</a> | <a href="` + u + `/wp-login.php">Log in</a></p>
<p id="backtoblog"><a href="` + u + `/">&larr; Go to ` + xmlEsc(h.siteName) + `</a></p>
</div>
</body>
</html>
`
	h.dyn(w, r, http.StatusOK, ctHTML, body, map[string]string{
		"Cache-Control":          "no-store, no-cache, must-revalidate, max-age=0",
		"X-Content-Type-Options": "nosniff",
	})
}

// onePixelGIF is a valid 43-byte 1x1 transparent GIF, the size WordPress' blank.gif
// stands in for.
var onePixelGIF = []byte{
	'G', 'I', 'F', '8', '9', 'a',
	0x01, 0x00, 0x01, 0x00, 0x80, 0x01, 0x00,
	0x00, 0x00, 0x00, 0xff, 0xff, 0xff,
	0x21, 0xf9, 0x04, 0x01, 0x00, 0x00, 0x01, 0x00, 0x2e, 0x00, 0x00,
	0x2c, 0x00, 0x00, 0x00, 0x00,
	0x01, 0x00, 0x01, 0x00, 0x00, 0x08, 0x02, 0x44, 0x00, 0x3b,
}

func (h *Handler) xmlrpc(w http.ResponseWriter, r *http.Request) {
	u := h.home(r)
	if (r.Method == http.MethodGet || r.Method == http.MethodHead) && r.URL.Query().Has("rsd") {
		rsd := `<?xml version="1.0" encoding="UTF-8"?>` + "\n" +
			`<rsd version="1.0" xmlns="http://archipelago.phrasewise.com/rsd">` + "\n" +
			`  <service>` + "\n" +
			`    <engineName>WordPress</engineName>` + "\n" +
			`    <engineLink>https://wordpress.org/</engineLink>` + "\n" +
			`    <homePageLink>` + xmlEsc(u) + `</homePageLink>` + "\n" +
			`    <apis>` + "\n" +
			`      <api name="WordPress" blogID="1" preferred="true" apiLink="` + xmlEsc(u) + `/xmlrpc.php" />` + "\n" +
			`      <api name="Blogger" blogID="1" preferred="false" apiLink="` + xmlEsc(u) + `/xmlrpc.php" />` + "\n" +
			`    </apis>` + "\n" +
			`  </service>` + "\n" +
			`</rsd>` + "\n"
		h.dyn(w, r, http.StatusOK, ctXML, rsd, nil)
		return
	}
	if r.Method != http.MethodPost {
		// WordPress core: status_header(405); header('Allow: POST');
		// header('Content-Type: text/plain'); die('XML-RPC server accepts POST requests only.')
		h.dyn(w, r, http.StatusMethodNotAllowed, "text/plain", "XML-RPC server accepts POST requests only.",
			map[string]string{"Allow": "POST"})
		return
	}
	data, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	h.xmlrpcMethod(w, r, string(data))
}

var wpMethods = []string{
	"blogger.getUsersBlogs", "blogger.getUserInfo", "blogger.getPost", "blogger.getRecentPosts",
	"metaWeblog.getPost", "metaWeblog.getRecentPosts", "mtwp.getTags",
	"wp.getUsersBlogs", "wp.getPage", "wp.getPages", "wp.newPost", "wp.editPost", "wp.deletePost",
	"wp.getPost", "wp.getPosts", "wp.getComment", "wp.getComments", "wp.getOptions", "wp.getCategories",
	"system.getCapabilities", "system.listMethods", "system.multicall",
}

func (h *Handler) xmlrpcMethod(w http.ResponseWriter, r *http.Request, body string) {
	method := xmlrpcMethodName(body)
	switch method {
	case "":
		h.dyn(w, r, http.StatusOK, ctXML, xmlrpcFault(-32700, "parse error. not well formed"), nil)
	case "system.listMethods":
		var b strings.Builder
		b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n<methodResponse><params><param><value><array><data>")
		for _, m := range wpMethods {
			b.WriteString("<value><string>" + xmlEsc(m) + "</string></value>")
		}
		b.WriteString("</data></array></value></param></params></methodResponse>\n")
		h.dyn(w, r, http.StatusOK, ctXML, b.String(), nil)
	case "system.getCapabilities":
		h.dyn(w, r, http.StatusOK, ctXML, xmlrpcFault(0, ""), nil)
	default:
		if strings.HasPrefix(method, "wp.") || strings.HasPrefix(method, "metaWeblog.") ||
			strings.HasPrefix(method, "blogger.") || strings.HasPrefix(method, "mt.") {
			h.dyn(w, r, http.StatusOK, ctXML, xmlrpcFault(403, "Incorrect username or password."), nil)
			return
		}
		h.dyn(w, r, http.StatusOK, ctXML, xmlrpcFault(-32601, "server error. requested method "+method+" does not exist."), nil)
	}
}

// xmlrpcMethodName pulls the called method out of a methodCall without a full XML
// parser; the shape is fixed and small.
func xmlrpcMethodName(body string) string {
	i := strings.Index(body, "<methodName>")
	if i < 0 {
		return ""
	}
	rest := body[i+len("<methodName>"):]
	j := strings.Index(rest, "</methodName>")
	if j < 0 {
		return ""
	}
	return strings.TrimSpace(rest[:j])
}

func xmlrpcFault(code int, msg string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>` + "\n<methodResponse><fault><value><struct>" +
		"<member><name>faultCode</name><value><int>" + strconv.Itoa(code) + "</int></value></member>" +
		"<member><name>faultString</name><value><string>" + xmlEsc(msg) + "</string></value></member>" +
		"</struct></value></fault></methodResponse>\n"
}

func (h *Handler) wpReadme(w http.ResponseWriter, r *http.Request) {
	u := h.home(r)
	body := `<!DOCTYPE html>
<html lang="en-US">
<head><meta charset="utf-8" /><title>WordPress &#8722; ReadMe</title><link rel="stylesheet" href="/wp-admin/load-styles.php?c=0&load%5Bchunk_0%5D=install&ver=` + h.ver + `" type="text/css" /></head>
<body class="light">
<h1 id="logo"><a href="https://wordpress.org/">WordPress</a></h1>
<p>Version <strong>` + h.ver + `</strong></p>
<h2>Requirements</h2>
<ul>
	<li><strong>PHP</strong> version <strong>7.2.24</strong> or greater.</li>
	<li><strong>MySQL</strong> version <strong>5.5.5</strong> or greater.</li>
</ul>
<h2>Installation</h2>
<ol>
	<li>Download and unpack the <a href="https://wordpress.org/download/">latest version</a>.</li>
	<li>Read the <a href="https://wordpress.org/documentation/article/faq-general/">documentation</a>.</li>
</ol>
<p><em>` + xmlEsc(u) + `</em></p>
</body>
</html>
`
	h.dyn(w, r, http.StatusOK, ctHTML, body, map[string]string{"Cache-Control": "no-cache"})
}

func (h *Handler) wpREST(w http.ResponseWriter, r *http.Request, name string) {
	if !(r.Method == http.MethodGet || r.Method == http.MethodHead) {
		h.methodNotAllowed(w)
		return
	}
	u := h.home(r)
	switch name {
	case "wp-json", "wp-json/":
		body := `{"name":` + jsonString(h.siteName) + `,"description":` + jsonString(h.siteName) +
			`,"url":"` + u + `/","home":"` + u + `/","gmt_offset":"0","timezone_string":"","namespaces":["oembed/1.0"],` +
			`"authentication":[],"routes":{"/":{"namespace":"","methods":["GET"]},"/wp/v2":{"namespace":"wp/v2","methods":["GET"]},` +
			`"/oembed/1.0":{"namespace":"oembed/1.0","methods":["GET"]}},"site_logo":{}}`
		h.dyn(w, r, http.StatusOK, ctJSON, body, nil)
	case "wp-json/wp/v2", "wp-json/wp/v2/":
		h.dyn(w, r, http.StatusOK, ctJSON, "[]", map[string]string{"X-WP-Total": "0", "X-WP-TotalPages": "0"})
	default:
		// Any other wp-json route: a valid but empty collection rather than a bare 404,
		// the way an unauthenticated request to most namespaces comes back.
		h.dyn(w, r, http.StatusOK, ctJSON, `{"code":"rest_no_route","message":"No route was found matching the URL and request method.","data":{"status":404}}`, nil)
	}
}

func jsonString(s string) string {
	if s == "" {
		s = "My WordPress Site"
	}
	return `"` + xmlEsc(s) + `"`
}

// serveWlwmanifest answers the wlwmanifest.xml the WordPress front page links to.
// It is a fixed, standard document.
func (h *Handler) serveWlwmanifest(w http.ResponseWriter, r *http.Request) {
	u := h.home(r)
	body := `<?xml version="1.0" encoding="UTF-8"?>` + "\n" +
		`<wlwmanifest version="1.0" xmlns="http://schemas.microsoft.com/wlwmanifest/xmlns">` + "\n" +
		`  <package version="6.2.0" date="2006-12-05"/>` + "\n" +
		`  <metagenerator template-version="1.2"/>` + "\n" +
		`  <service name="WordPress" version="` + xmlEsc(h.ver) + `" servicedescription="` + xmlEsc(u) + `" uniquename="wp." maxfeeds="50" published="yes" logo="` + u + `/wp-includes/images/blank.gif" bgimage="" around="" default="blog" ping="" pingback="` + u + `/xmlrpc.php" usejs="1" client="metaWeblog" generator="` + u + `/wp-admin/post.php">` + "\n" +
		`    <homeretry>3600</homeretry><urltype>path</urltype><pageservice blogid="1" url="` + u + `/" homepagename="Go to ` + xmlEsc(h.siteName) + `" homepage="` + u + `/" onclick="newPost" editscript="true" preview="yes"/>` + "\n" +
		`  </service>` + "\n" +
		`  <xmlrpc><blogid><value>1</value></blogid><url>` + xmlEsc(u) + `/xmlrpc.php</url></xmlrpc>` + "\n" +
		`</wlwmanifest>` + "\n"
	h.dyn(w, r, http.StatusOK, ctXML, body, nil)
}

// serveRSS is a minimal-but-valid RSS 2.0 / Atom feed naming the site and listing
// only URLs that actually resolve (the front page), so a feed never cites a dead post.
func (h *Handler) serveRSS(w http.ResponseWriter, r *http.Request, atom, comments bool) {
	u := h.home(r)
	title := h.siteName
	if title == "" {
		title = "Site Feed"
	}
	if comments {
		title += " Comments Feed"
	} else {
		title += " Feed"
	}
	gen := h.engineName()
	self := u + "/feed/"
	if h.persona != pWordPress {
		self = u + "/rss.xml"
	}
	if atom {
		body := `<?xml version="1.0" encoding="UTF-8"?>` + "\n" +
			`<feed xmlns="http://www.w3.org/2005/Atom" xml:lang="en-US">` + "\n" +
			`  <title>` + xmlEsc(title) + `</title><subtitle level="stage"/>` + "\n" +
			`  <link rel="alternate" type="text/html" hreflang="en-US" href="` + xmlEsc(u) + `"/>` + "\n" +
			`  <link rel="self" type="application/atom+xml" href="` + xmlEsc(self) + `"/>` + "\n" +
			`  <updated>` + h.feedDate() + `</updated><generator>` + xmlEsc(gen) + `</generator>` + "\n" +
			`</feed>` + "\n"
		h.dyn(w, r, http.StatusOK, "application/atom+xml; charset=UTF-8", body, map[string]string{"Link": "<" + self + ">; rel=self"})
		return
	}
	body := `<?xml version="1.0" encoding="UTF-8"?>` + "\n" +
		`<rss version="2.0" xmlns:content="http://purl.org/rss/1.0/modules/content/" xmlns:atom="http://www.w3.org/2005/Atom" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:sy="http://api.syndic8.com/" xmlns:slash="http://purl.org/rss/1.0/modules/slash/">` + "\n" +
		`<channel>` + "\n" +
		`  <title>` + xmlEsc(title) + `</title>` + "\n" +
		`  <atom:link href="` + xmlEsc(self) + `" rel="self" type="application/rss+xml"/>` + "\n" +
		`  <link>` + xmlEsc(u) + `</link>` + "\n" +
		`  <description>` + xmlEsc(title) + `</description>` + "\n" +
		`  <lastBuildDate>` + h.feedDate() + `</lastBuildDate>` + "\n" +
		`  <language>en-US</language>` + "\n" +
		`  <generator>` + xmlEsc(gen) + `</generator>` + "\n" +
		`</channel>` + "\n" +
		`</rss>` + "\n"
	h.dyn(w, r, http.StatusOK, ctXML, body, map[string]string{"Vary": "Accept"})
}

func (h *Handler) feedDate() string {
	return h.index.modTime.UTC().Format(time.RFC1123Z)
}

// serveSitemap is a valid urlset containing exactly the pages this install really
// serves — the front page — so the robots.txt Sitemap line is never a dead promise.
func (h *Handler) serveSitemap(w http.ResponseWriter, r *http.Request) {
	if !(r.Method == http.MethodGet || r.Method == http.MethodHead) {
		h.methodNotAllowed(w)
		return
	}
	u := h.home(r)
	lastmod := h.index.modTime.UTC().Format("2006-01-02T15:04:05+00:00")
	comment := ""
	if h.persona != pStatic {
		comment = "<!-- sitemap generated by " + xmlEsc(h.engineName()) + " -->\n"
	}
	body := `<?xml version="1.0" encoding="UTF-8"?>` + "\n" +
		comment +
		`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n" +
		`  <url><loc>` + xmlEsc(u) + `/</loc><lastmod>` + lastmod + `</lastmod><changefreq>daily</changefreq><priority>1.0</priority></url>` + "\n" +
		`</urlset>` + "\n"
	h.dyn(w, r, http.StatusOK, ctXML, body, nil)
}

func (h *Handler) engineName() string {
	switch h.persona {
	case pWordPress:
		return "WordPress " + h.ver
	case pJoomla:
		return "Joomla! CMS"
	case pDrupal:
		return h.ver
	case pBitrix:
		return "1C-Bitrix " + h.ver
	case pDLE:
		return "DataLife Engine " + h.ver
	default:
		return "static"
	}
}

func (h *Handler) blockDir(w http.ResponseWriter, r *http.Request) {
	h.dyn(w, r, http.StatusForbidden, ctHTML, "<html>\r\n<head><title>403 Forbidden</title></head>\r\n<body>\r\n<center><h1>403 Forbidden</h1></center>\r\n</body>\r\n</html>\r\n", nil)
}

func (h *Handler) methodNotAllowed(w http.ResponseWriter) {
	w.Header().Set("Allow", "GET, HEAD, POST")
	w.Header().Set("Content-Length", "0")
	w.WriteHeader(http.StatusMethodNotAllowed)
}

// persona404 is the not-found page a CMS box answers a guessed path with, so an
// unknown URL behaves like the engine it advertises instead of falling back to the
// front page (the single-page-app tell). It stays a real 404 carrying the persona's
// generator marker, and its bytes differ from the index.
func (h *Handler) persona404(w http.ResponseWriter, r *http.Request) {
	u := h.home(r)
	gen := ""
	switch h.persona {
	case pWordPress:
		gen = "<meta name=\"generator\" content=\"WordPress " + xmlEsc(h.ver) + "\" />"
	case pJoomla:
		gen = "<meta name=\"generator\" content=\"Joomla! - Open Source Content Management\" />"
	case pDrupal:
		gen = "<meta name=\"Generator\" content=\"" + xmlEsc(h.ver) + "\" />"
	case pDLE:
		gen = "<meta name=\"generator\" content=\"DataLife Engine (v" + xmlEsc(h.ver) + ")\" />"
	}
	heading := "404 &mdash; Not Found"
	if h.persona == pWordPress {
		heading = xmlEsc(h.siteName) + " &raquo; Page not found"
	}
	body := `<!DOCTYPE html>
<html lang="en-US">
<head><meta charset="UTF-8" /><meta name="viewport" content="width=device-width, initial-scale=1" />` + gen + `<title>` + heading + `</title></head>
<body class="error404">
<div class="container">
<h1 class="page-title">Not Found</h1>
<p>The page you requested could not be found. Try returning to the <a href="` + u + `/">home page</a>.</p>
</div>
</body>
</html>
`
	h.dyn(w, r, http.StatusNotFound, ctHTML, body, map[string]string{"X-Content-Type-Options": "nosniff"})
}

// urlEncode is the form used by CMS redirect_to parameters (Go's QueryEscape is
// close enough and keeps the escaping consistent everywhere it is needed).
func urlEncode(s string) string { return queryEscape(s) }

func queryEscape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.' || c == '~':
			b.WriteByte(c)
		default:
			const hex = "0123456789ABCDEF"
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&0xF])
		}
	}
	return b.String()
}

// ---- Joomla.

func (h *Handler) joomlaServe(w http.ResponseWriter, r *http.Request, name string) bool {
	read := r.Method == http.MethodGet || r.Method == http.MethodHead
	switch {
	case name == "index.php":
		if !read {
			return false
		}
		h.serve(w, r, h.index, http.StatusOK)
		return true
	case name == "sitemap.xml":
		h.serveSitemap(w, r)
		return true
	case name == "administrator":
		// Same trailing-slash split as wp-admin: the bare path gains the slash, the
		// slashed path renders the login. One answer for both would loop.
		if strings.HasSuffix(r.URL.Path, "/") {
			h.joomlaLogin(w, r)
			return true
		}
		h.redirect(w, r, http.StatusMovedPermanently, h.home(r)+"/administrator/")
		return true
	case name == "administrator/index.php":
		h.joomlaLogin(w, r)
		return true
	case strings.HasPrefix(name, "administrator/"):
		h.redirect(w, r, http.StatusFound, h.home(r)+"/administrator/")
		return true
	case name == "cache", name == "components", name == "modules", name == "plugins",
		strings.HasPrefix(name, "cache/"), strings.HasPrefix(name, "components/"),
		strings.HasPrefix(name, "modules/"), strings.HasPrefix(name, "plugins/"):
		h.blockDir(w, r)
		return true
	}
	return false
}

func (h *Handler) joomlaLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodDelete || r.Method == http.MethodPut {
		h.methodNotAllowed(w)
		return
	}
	u := h.home(r)
	body := `<!DOCTYPE html>
<html lang="en-gb" dir="ltr">
<head><meta charset="utf-8" /><meta name="viewport" content="width=device-width, initial-scale=1" /><meta name="robots" content="noindex, nofollow" /><title>Home &#8212; Joomla! Administrator</title></head>
<body class="admin login view-login">
<div id="j-main-container">
<form action="` + u + `/administrator/index.php" method="post" id="form-login" class="form-vertical">
	<fieldset>
	<legend>Please enter your user name and password</legend>
	<div class="control-group"><label for="username">User Name</label><input type="text" name="username" id="username" class="required" tabindex="1" autofocus /></div>
	<div class="control-group"><label for="password">Password</label><input type="password" name="password" id="password" class="required" tabindex="2" /></div>
	<div class="control-group"><button type="submit" class="btn btn-primary" name="Submit" tabindex="4">Log in</button></div>
	<input type="hidden" name="6d543f8a0698fe12e2f5bfc1c5c5f6f3" value="1" /></fieldset>
</form>
<p><a href="` + u + `/">Go to public site</a></p>
</div>
</body>
</html>
`
	h.dyn(w, r, http.StatusOK, ctHTML, body, map[string]string{"X-Content-Type-Options": "nosniff"})
}

// ---- Drupal.

func (h *Handler) drupalServe(w http.ResponseWriter, r *http.Request, name string) bool {
	read := r.Method == http.MethodGet || r.Method == http.MethodHead
	switch {
	case name == "index.php":
		if !read {
			return false
		}
		h.serve(w, r, h.index, http.StatusOK)
		return true
	case name == "sitemap.xml":
		h.serveSitemap(w, r)
		return true
	case name == "rss.xml":
		h.serveRSS(w, r, false, false)
		return true
	case name == "user":
		h.redirect(w, r, http.StatusFound, h.home(r)+"/user/login")
		return true
	case strings.HasPrefix(name, "user/"):
		h.drupalLogin(w, r)
		return true
	case name == "search", strings.HasPrefix(name, "search/"):
		// robots.txt names /search/node, so it answers like Drupal's search route:
		// the form page, generator tag and all.
		if !read {
			h.methodNotAllowed(w)
			return true
		}
		h.drupalSearch(w, r, r.URL.Query().Get("keys"))
		return true
	case name == "core", strings.HasPrefix(name, "core/"), name == "modules", strings.HasPrefix(name, "modules/"),
		name == "themes", strings.HasPrefix(name, "themes/"), name == "profiles", strings.HasPrefix(name, "profiles/"):
		h.blockDir(w, r)
		return true
	}
	return false
}

func (h *Handler) drupalLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodDelete || r.Method == http.MethodPut {
		h.methodNotAllowed(w)
		return
	}
	u := h.home(r)
	body := `<!DOCTYPE html>
<html lang="en" dir="ltr">
<head><meta charset="utf-8" /><meta name="Generator" content="` + xmlEsc(h.ver) + `" /><meta name="robots" content="noindex, nofollow" /><title>Log in | ` + xmlEsc(h.siteName) + `</title></head>
<body class="path-user">
<main role="main"><a id="main-content" tabindex="-1"></a>
<div class="region-content"><h1 class="page-title">Log in</h1>
<form action="` + u + `/user/login" method="post" id="user-login-form">
	<div class="js-form-item form-item js-form-type-textfield"><label for="edit-name">Username or email address</label><input data-drupal-selector="edit-name" type="text" id="edit-name" name="name" size="60" maxlength="60" required autocapitalize="none" /></div>
	<div class="js-form-item form-item js-form-type-password"><label for="edit-pass">Password</label><input data-drupal-selector="edit-pass" type="password" id="edit-pass" name="pass" size="60" maxlength="128" required /></div>
	<div class="form-actions js-form-wrapper"><button data-drupal-selector="edit-submit" type="submit" id="edit-submit" class="button button--primary js-form-submit form-submit">Log in</button></div>
	<input autocomplete="off" type="hidden" name="form_build_id" data-form-build-id value="form-` + xmlEsc(h.siteToken()) + `" /><input autocomplete="off" type="hidden" name="form_id" value="user_login_form" />
</form>
<p><a href="` + u + `/user/password">Reset your password</a></p>
</div></div></main>
</body>
</html>
`
	h.dyn(w, r, http.StatusOK, ctHTML, body, map[string]string{"X-Generator": h.ver})
}

// drupalSearch is the /search/node route robots.txt names. With an empty site
// index a real Drupal answers a keys query with "yielded no results", which is
// exactly what this renders — no promise of content the box does not have.
func (h *Handler) drupalSearch(w http.ResponseWriter, r *http.Request, keys string) {
	u := h.home(r)
	result := ""
	if keys != "" {
		result = `<h2 class="no-results">Your search yielded no results.</h2>` + "\n"
	}
	body := `<!DOCTYPE html>
<html lang="en" dir="ltr">
<head><meta charset="utf-8" /><meta name="Generator" content="` + xmlEsc(h.ver) + `" /><meta name="robots" content="noindex, follow" /><title>Search | ` + xmlEsc(h.siteName) + `</title></head>
<body class="path-search">
<main role="main"><h1 class="page-title">Search</h1>
<form action="` + u + `/search/node" method="get" id="search-form">
	<div class="js-form-item form-item"><label for="edit-keys" class="visually-hidden">Enter your keywords</label><input type="search" id="edit-keys" name="keys" size="30" maxlength="255" value="` + xmlEsc(keys) + `" /></div>
	<div class="form-actions"><button type="submit" id="edit-submit" class="button">Search</button></div>
</form>
` + result + `</main>
</body>
</html>
`
	h.dyn(w, r, http.StatusOK, ctHTML, body, map[string]string{"X-Generator": h.ver})
}

// ---- Bitrix.

func (h *Handler) bitrixServe(w http.ResponseWriter, r *http.Request, name string) bool {
	read := r.Method == http.MethodGet || r.Method == http.MethodHead
	switch {
	case name == "index.php":
		if !read {
			return false
		}
		h.serve(w, r, h.index, http.StatusOK)
		return true
	case name == "sitemap.xml":
		h.serveSitemap(w, r)
		return true
	case name == "bitrix", strings.HasPrefix(name, "bitrix/"):
		h.blockDir(w, r)
		return true
	case name == "upload", strings.HasPrefix(name, "upload/"):
		h.blockDir(w, r)
		return true
	case name == "personal", strings.HasPrefix(name, "personal/"):
		h.redirect(w, r, http.StatusFound, h.home(r)+"/auth/?backurl="+urlEncode("/personal/"))
		return true
	case name == "auth", strings.HasPrefix(name, "auth/"):
		h.bitrixLogin(w, r)
		return true
	}
	return false
}

func (h *Handler) bitrixLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodDelete || r.Method == http.MethodPut {
		h.methodNotAllowed(w)
		return
	}
	u := h.home(r)
	body := `<!DOCTYPE html>
<html lang="ru">
<head><meta http-equiv="X-UA-Compatible" content="IE=edge" /><meta http-equiv="Content-Type" content="text/html; charset=UTF-8" /><meta name="viewport" content="width=device-width, initial-scale=1" /><title>Вход на сайт &#8212; ` + xmlEsc(h.siteName) + `</title></head>
<body>
<form action="` + u + `/auth/" method="post">
	<input type="hidden" name="backurl" value="/personal/" /><input type="hidden" name="AUTH_FORM" value="Y" /><input type="hidden" name="TYPE" value="AUTH" />
	<label for="form_login-18">Логин или e-mail</label><input type="text" name="USER_LOGIN" id="form_login-18" size="30" value="" autocomplete="username" />
	<label for="form_password-19">Пароль</label><input type="password" name="USER_PASSWORD" id="form_password-19" size="30" autocomplete="current-password" />
	<label><input type="checkbox" name="REMEMBER" value="Y" /> Запомнить меня</label>
	<input type="submit" name="Login" value="Вход" />
</form>
</body>
</html>
`
	h.dyn(w, r, http.StatusOK, ctHTML, body, map[string]string{"Set-Cookie": "BITRIX_SM_SSID_HASH=; path=/; HttpOnly"})
}

// ---- DataLife Engine.

func (h *Handler) dleServe(w http.ResponseWriter, r *http.Request, name string) bool {
	read := r.Method == http.MethodGet || r.Method == http.MethodHead
	switch {
	case r.URL.Query().Get("do") == "opensearch":
		h.dleOpensearch(w, r)
		return true
	case r.URL.Query().Get("do") == "rss", name == "rss.xml", name == "last_rss", name == "info_rss":
		h.serveRSS(w, r, false, false)
		return true
	case name == "index.php":
		// DLE's own search and post forms target /index.php; always render the site.
		h.serve(w, r, h.index, http.StatusOK)
		return true
	case name == "sitemap.xml":
		h.serveSitemap(w, r)
		return true
	case name == "admin.php":
		h.dleLogin(w, r)
		return true
	case name == "login.php":
		// The login form on every DLE page posts here; answering it keeps the
		// markup's promise. Without credentials DLE re-renders the login shell.
		h.dleLogin(w, r)
		return true
	case name == "engine", strings.HasPrefix(name, "engine/"):
		h.blockDir(w, r)
		return true
	case name == "statistics.html" || name == "statistics" || strings.HasPrefix(name, "statistics/"):
		// DLE renders the top-page statistics as a normal engine page; robots.txt
		// names statistics.html, so it must carry content, not another redirect.
		if !read {
			h.methodNotAllowed(w)
			return true
		}
		h.dleStatistics(w, r)
		return true
	}
	return false
}

func (h *Handler) dleOpensearch(w http.ResponseWriter, r *http.Request) {
	u := h.home(r)
	body := `<?xml version="1.0" encoding="UTF-8"?>` + "\n" +
		`<OpenSearchDescription xmlns="http://a9.com/-/spec/opensearch/1.1/">` + "\n" +
		`  <ShortName>` + xmlEsc(h.siteName) + `</ShortName><Description>` + xmlEsc(h.siteName) + `</Description>` + "\n" +
		`  <InputEncoding>UTF-8</InputEncoding><Tags>DLE search</Tags>` + "\n" +
		`  <Url type="text/html" method="post" template="` + u + `/index.php?do=search" />` + "\n" +
		`</OpenSearchDescription>` + "\n"
	h.dyn(w, r, http.StatusOK, "application/opensearchdescription+xml; charset=UTF-8", body, nil)
}

func (h *Handler) dleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodDelete || r.Method == http.MethodPut {
		h.methodNotAllowed(w)
		return
	}
	u := h.home(r)
	body := `<!DOCTYPE html>
<html lang="ru">
<head><meta http-equiv="Content-Type" content="text/html; charset=UTF-8" /><meta name="generator" content="DataLife Engine (v` + xmlEsc(h.ver) + `)" /><title>Вход на сайт</title></head>
<body>
<div class="dle-login"><div class="title">Форма входа</div>
<form action="` + u + `/login.php" method="post">
	<input type="hidden" name="do" value="login" /><input type="hidden" name="subaction" value="login" />
	<label for="login_field">Логин:</label><input class="loginfield" type="text" name="login" id="login_field" /><br />
	<label for="password_field">Пароль:</label><input class="passwordfield" type="password" name="password" id="password_field" /><br />
	<input type="checkbox" name="cookie" id="cookie" value="1" checked /><label for="cookie">Запомнить меня</label><br />
	<input type="submit" value="Вход" name="submit" /><input type="hidden" name="user_search" value="0" />
</form>
</div>
</body>
</html>
`
	h.dyn(w, r, http.StatusOK, ctHTML, body, nil)
}

// dleStatistics is the engine's public "top" page, statistics.html: DLE generates it
// as a plain page with the site counters, so robots.txt can name it and it answers.
func (h *Handler) dleStatistics(w http.ResponseWriter, r *http.Request) {
	u := h.home(r)
	news := 10 + h.st.u64("stat-news")%90
	comments := 20 + h.st.u64("stat-comments")%400
	body := `<!DOCTYPE html>
<html lang="ru">
<head><meta http-equiv="Content-Type" content="text/html; charset=UTF-8" /><meta name="generator" content="DataLife Engine (v` + xmlEsc(h.ver) + `)" /><title>Статистика сайта &#8212; ` + xmlEsc(h.siteName) + `</title></head>
<body>
<div class="statistics"><div class="title">Статистика сайта</div>
<p>Всего материалов: <b>` + strconv.FormatUint(news, 10) + `</b><br />
Всего комментариев: <b>` + strconv.FormatUint(comments, 10) + `</b><br />
Пользователей: <b>` + strconv.FormatUint(3+h.st.u64("stat-users")%40, 10) + `</b></p>
<p><a href="` + u + `/">Вернуться на главную</a></p>
</div>
</body>
</html>
`
	h.dyn(w, r, http.StatusOK, ctHTML, body, nil)
}

// ---- Nextcloud.

func (h *Handler) nextcloudServe(w http.ResponseWriter, r *http.Request, name string) bool {
	read := r.Method == http.MethodGet || r.Method == http.MethodHead
	switch {
	case name == "index.php", name == "index.php/login":
		h.serve(w, r, h.index, http.StatusOK)
		return true
	case strings.HasPrefix(name, "index.php/"):
		// Every unauthenticated Nextcloud app route bounces to the login page; a 404
		// there would be the odd answer for this engine.
		h.redirect(w, r, http.StatusSeeOther, h.home(r)+"/index.php/login")
		return true
	case name == "login":
		h.redirect(w, r, http.StatusSeeOther, h.home(r)+"/index.php/login")
		return true
	case name == "status.php":
		h.ncStatus(w, r)
		return true
	case name == "remote.php", strings.HasPrefix(name, "remote.php/"), strings.HasPrefix(name, "ocs/"):
		h.ncUnauthorized(w, r)
		return true
	case name == ".well-known/caldav", name == ".well-known/carddav", name == ".well-known/webfinger":
		h.redirect(w, r, http.StatusMovedPermanently, h.home(r)+"/remote.php/dav")
		return true
	}
	_ = read
	return false
}

func nextcloudOwns(name string) bool {
	switch {
	case name == "index.php", name == "index.php/login", name == "login", name == "status.php",
		name == ".well-known/caldav", name == ".well-known/carddav", name == ".well-known/webfinger":
		return true
	}
	return strings.HasPrefix(name, "remote.php") || strings.HasPrefix(name, "ocs/") ||
		strings.HasPrefix(name, "index.php/")
}

func (h *Handler) ncStatus(w http.ResponseWriter, r *http.Request) {
	major := strings.SplitN(h.ver, ".", 2)[0]
	if major == "" {
		major = "30"
	}
	body := `{"installed":true,"maintenance":false,"needsDbUpgrade":false,"version":"` + h.ver +
		`","versionstring":"` + major + `","edition":"","productname":"Nextcloud","extended_support":true}` + "\n"
	h.dyn(w, r, http.StatusOK, ctJSON, body, map[string]string{"X-Request-Id": h.siteToken()})
}

func (h *Handler) ncUnauthorized(w http.ResponseWriter, r *http.Request) {
	h.dyn(w, r, http.StatusUnauthorized, ctHTML,
		`<!DOCTYPE html><html class="ng-csp"><head><meta charset="utf-8"><title>401 Unauthorized</title></head><body>Unauthorized</body></html>`,
		map[string]string{"WWW-Authenticate": `Basic realm="nextcloud", charset="UTF-8"`})
}

// siteToken is a short stable hex tag from the stamp, used where a real engine
// would emit an opaque per-install value (a form id, a request id).
func (h *Handler) siteToken() string { return strconv.FormatUint(h.st.u64("token"), 16) }
