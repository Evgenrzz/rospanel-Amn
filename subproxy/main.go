// subproxy serves the operator's subscription page and fronts the panel for it.
//
// The page is static and data-driven: it asks this proxy for the user's subscription
// View (which only an admin API key can read) and asks it to re-send the page's
// mutating actions (pay, promo, autorenew, unbind) to the panel's token-scoped sub
// endpoints, adding the X-RosPanel-Sub header a browser cannot set cross-origin.
//
// Everything is configuration, no state:
//
//	SUBPROXY_ADDR    listen address            (default :8443)
//	SUBPROXY_CERT    TLS certificate file      (empty = plain HTTP)
//	SUBPROXY_KEY     TLS private key file      (reloaded on every handshake)
//	SUBPROXY_PANEL   panel base URL            (default https://amn.amanesia.vip)
//	SUBPROXY_API     base URL of the /v1 surface (default SUBPROXY_PANEL); the panel
//	                 mounts it under an unguessable segment, so it is not PANEL + /v1
//	SUBPROXY_APIKEY  panel API key (users.view)
//	SUBPROXY_SUBBASE subscription base, the sub URL minus the token
//	                 (e.g. https://host/sub); empty = ask the API for it
//	SUBPROXY_STATIC  directory with index.html (default ./static)
//
// The page it serves is ../subpage/index-redesign.html; _sp_uid.ps1 builds this and
// installs both on the master (see FORK-CUSTOM.md §7).
package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

var (
	addr     = envOr("SUBPROXY_ADDR", ":8443")
	certF    = os.Getenv("SUBPROXY_CERT")
	keyF     = os.Getenv("SUBPROXY_KEY")
	panel    = strings.TrimRight(envOr("SUBPROXY_PANEL", "https://amn.amanesia.vip"), "/")
	apiBase  = strings.TrimRight(envOr("SUBPROXY_API", panel), "/")
	apiKey   = os.Getenv("SUBPROXY_APIKEY")
	subBase0 = strings.TrimRight(os.Getenv("SUBPROXY_SUBBASE"), "/")
	staticD  = envOr("SUBPROXY_STATIC", "./static")

	tokenRe = regexp.MustCompile(`^[A-Za-z0-9_-]{8,128}$`)

	// The only sub leaves the page may drive. Everything else the panel offers on
	// the subscription path stays unreachable through us.
	allowedLeaf = map[string]bool{
		"pay": true, "cancel": true, "order": true,
		"topup": true, "promo": true, "autorenew": true,
		"devices/unbind": true,
	}

	baseCache sync.Map // token -> subBase{base, at}
)

type subBase struct {
	base string
	at   time.Time
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func main() {
	if apiKey == "" {
		log.Fatal("SUBPROXY_APIKEY is required")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/view", handleView)
	mux.HandleFunc("/api/sub/", handleAction)
	mux.HandleFunc("/", handleStatic)

	srv := &http.Server{Addr: addr, Handler: withHeaders(mux), ReadHeaderTimeout: 10 * time.Second}
	if certF != "" && keyF != "" {
		// Reload on every handshake so a panel cert renewal takes effect without us.
		srv.TLSConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
			GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
				c, err := tls.LoadX509KeyPair(certF, keyF)
				return &c, err
			},
		}
		log.Printf("subproxy listening on %s (tls) -> %s", addr, apiBase)
		log.Fatal(srv.ListenAndServeTLS("", ""))
	}
	log.Printf("subproxy listening on %s (plain) -> %s", addr, apiBase)
	log.Fatal(srv.ListenAndServe())
}

// withHeaders keeps the subscription token out of referrers: the token sits in our
// own URL and the page links to external app downloads, so nothing may be sent along.
func withHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Robots-Tag", "noindex, nofollow")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// handleStatic serves the page itself: a fixed set of files, nothing else.
// Every relative URL the page uses (api/…, logo.png, fonts/…) resolves against the
// root, so "/" is the whole app.
func handleStatic(w http.ResponseWriter, r *http.Request) {
	name := map[string]string{
		"/":                               "index.html",
		"/index.html":                     "index.html",
		"/mock.js":                        "mock.js",
		"/mock-redesign.js":               "mock.js", // demo data is local-only; serve the empty stub
		"/logo.png":                       "logo.png",
		"/bg-dragon.png":                  "bg-dragon.png",
		"/apple-touch-icon.png":           "apple-touch-icon.png",
		"/fonts/manrope-cyrillic.woff2":   "fonts/manrope-cyrillic.woff2",
		"/fonts/manrope-latin.woff2":      "fonts/manrope-latin.woff2",
		"/fonts/onest-cyrillic.woff2":     "fonts/onest-cyrillic.woff2",
		"/fonts/onest-latin.woff2":        "fonts/onest-latin.woff2",
		"/fonts/unbounded-cyrillic.woff2": "fonts/unbounded-cyrillic.woff2",
		"/fonts/unbounded-latin.woff2":    "fonts/unbounded-latin.woff2",
		"/favicon.svg":                    "favicon.svg",
		"/favicon.ico":                    "favicon.svg",
	}[r.URL.Path]
	if strings.HasPrefix(name, "fonts/") || name == "logo.png" || name == "bg-dragon.png" || name == "apple-touch-icon.png" {
		w.Header().Set("Cache-Control", "public, max-age=604800")
	}
	if name == "" {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, filepath.Join(staticD, name))
}

// handleView answers the page's request for the user's subscription as data.
func handleView(w http.ResponseWriter, r *http.Request) {
	tok := r.URL.Query().Get("t")
	if !tokenRe.MatchString(tok) {
		http.Error(w, `{"error":"bad token"}`, http.StatusBadRequest)
		return
	}
	id, err := userIDByToken(tok)
	if err != nil {
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		return
	}
	lang := r.URL.Query().Get("lang")
	if lang == "" {
		lang = "ru"
	}
	body, code, err := panelGet("/v1/users/" + itoa(id) + "/subscription?lang=" + url.QueryEscape(lang))
	if err != nil {
		http.Error(w, `{"error":"upstream"}`, http.StatusBadGateway)
		return
	}
	// The view carries the user's name but no id, and the page shows both, so add
	// the identity we already resolved on the way in. Only a view we can parse is
	// touched — anything else goes through exactly as the panel sent it.
	if code == http.StatusOK {
		body = withIdentity(body, id)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_, _ = w.Write(body)
}

// withIdentity adds user_id to the view's data object, leaving every other byte of
// the panel's answer alone. Numbers stay as written (UseNumber) so a timestamp does
// not come back out in exponent form.
func withIdentity(body []byte, id int64) []byte {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var env map[string]any
	if dec.Decode(&env) != nil {
		return body
	}
	data, ok := env["data"].(map[string]any)
	if !ok {
		return body
	}
	data["user_id"] = id
	out, err := json.Marshal(env)
	if err != nil {
		return body
	}
	return out
}

// handleAction re-sends one of the page's actions to the panel's sub endpoint.
func handleAction(w http.ResponseWriter, r *http.Request) {
	leaf := strings.TrimPrefix(r.URL.Path, "/api/sub/")
	if !allowedLeaf[leaf] {
		http.Error(w, `{"error":"unknown action"}`, http.StatusNotFound)
		return
	}
	// "order" is the panel's GET probe for a pending provider payment; every other
	// leaf mutates state and only accepts POST.
	if r.Method != http.MethodPost && !(r.Method == http.MethodGet && leaf == "order") {
		http.Error(w, `{"error":"method"}`, http.StatusMethodNotAllowed)
		return
	}
	tok := r.URL.Query().Get("t")
	if !tokenRe.MatchString(tok) {
		http.Error(w, `{"error":"bad token"}`, http.StatusBadRequest)
		return
	}
	base, err := subURLFor(tok)
	if err != nil {
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		return
	}
	var body []byte
	if r.Method == http.MethodPost {
		body, _ = io.ReadAll(io.LimitReader(r.Body, 1<<20))
	}

	// The sub URL we hold is the panel's PUBLIC address; re-send the action to the
	// panel we are configured to talk to (on the master that is the loopback admin
	// listener), keeping only the path.
	target := base + "/" + leaf
	if u, perr := url.Parse(target); perr == nil {
		target = panel + u.RequestURI()
	}
	var rdr io.Reader
	if r.Method == http.MethodPost {
		rdr = strings.NewReader(string(body))
	}
	req, err := http.NewRequest(r.Method, target, rdr)
	if err != nil {
		http.Error(w, `{"error":"upstream"}`, http.StatusBadGateway)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-RosPanel-Sub", "1") // proves to the panel the call came from its page
	req.Header.Set("Accept-Language", r.Header.Get("Accept-Language"))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, `{"error":"upstream"}`, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(out)
}

// userIDByToken resolves a subscription token to the user's id via the admin API.
func userIDByToken(tok string) (int64, error) {
	body, code, err := panelGet("/v1/users?sub_token=" + url.QueryEscape(tok))
	if err != nil || code != http.StatusOK {
		return 0, errNoUser
	}
	var resp struct {
		Data []struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &resp) != nil || len(resp.Data) == 0 {
		return 0, errNoUser
	}
	return resp.Data[0].ID, nil
}

// subURLFor is the user's own subscription URL (…/sub/<token>), which is what every
// sub leaf hangs off: …/sub/<token>/pay. Configured when the operator knows the base
// (host + sub path never change), otherwise learned from the API and cached.
func subURLFor(tok string) (string, error) {
	if subBase0 != "" {
		return subBase0 + "/" + tok, nil
	}
	if v, ok := baseCache.Load(tok); ok {
		if sb := v.(subBase); time.Since(sb.at) < 10*time.Minute {
			return sb.base, nil
		}
	}
	id, err := userIDByToken(tok)
	if err != nil {
		return "", err
	}
	body, code, err := panelGet("/v1/users/" + itoa(id) + "/subscription?lang=ru")
	if err != nil || code != http.StatusOK {
		return "", err
	}
	var resp struct {
		Data struct {
			SubURL string `json:"sub_url"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &resp) != nil || resp.Data.SubURL == "" {
		return "", errNoUser
	}
	base := strings.TrimSuffix(resp.Data.SubURL, "/")
	if !strings.HasSuffix(base, "/"+tok) {
		return "", errNoUser
	}
	baseCache.Store(tok, subBase{base: base, at: time.Now()})
	return base, nil
}

func panelGet(path string) ([]byte, int, error) {
	req, err := http.NewRequest(http.MethodGet, apiBase+path, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return body, resp.StatusCode, nil
}

type errString string

func (e errString) Error() string { return string(e) }

const errNoUser = errString("no such user")

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
