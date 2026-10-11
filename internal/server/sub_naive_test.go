package server

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
)

// The naive lane is node-only: the master never runs caddy-naive, so its /naive/0.*
// endpoint is the decoy and the page offers no master card. For a node that has joined
// with the lane on, /naive/<id>.conf hands out this user's client config (minting the
// password on the way), the .png is its QR, an unknown or malformed id is the decoy,
// and once the password exists the page carries the card and the base64 list the link.
func TestNaiveConfigEndpointsAndPageCard(t *testing.T) {
	t.Parallel()
	h, mgr, st := nodeAPITestServer(t)

	node, err := mgr.CreateNode("naive-node", "nl.example.com")
	if err != nil {
		t.Fatalf("create node: %v", err)
	}
	if err := st.SetNodeEnabled(node.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := st.SetNodeNaiveEnabled(node.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := st.SetNodeConnections(node.ID, &model.NodeConnections{NaivePort: 2053, NaiveName: "Naive NL"}); err != nil {
		t.Fatal(err)
	}
	// A node that has reported in with a pinned cert, so NodeLinkSettings lists it.
	if err := st.UpdateNodeStatus(node.ID, model.NodeStatusUpdate{
		LastSeen: time.Now().Unix(), CertSHA256: "aa:bb:cc",
	}); err != nil {
		t.Fatal(err)
	}

	u, err := mgr.CreateUser(t.Context(), "naive", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	get := func(path, ua string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("User-Agent", ua)
		if strings.HasPrefix(ua, "Mozilla") { // a browser asks for the page, not the payload
			req.Header.Set("Accept", "text/html,application/xhtml+xml")
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	base := "/sub/" + u.SubToken
	confURL := fmt.Sprintf("%s/naive/%d.conf", base, node.ID)
	pngURL := fmt.Sprintf("%s/naive/%d.png", base, node.ID)

	// The master, an unknown server and a malformed id are all the decoy — none of them
	// may leak a client config.
	for _, bad := range []string{base + "/naive/0.conf", base + "/naive/77.conf", base + "/naive/x.conf"} {
		if body := get(bad, "curl/8").Body.String(); strings.Contains(body, `"proxy"`) {
			t.Errorf("naive config served for %q", bad)
		}
	}

	// The real node endpoint: the {"proxy":"https://…"} JSON, on the node's host and port.
	conf := get(confURL, "curl/8")
	if conf.Code != http.StatusOK {
		t.Fatalf("config status = %d\n%s", conf.Code, conf.Body.String())
	}
	var doc struct {
		Proxy string `json:"proxy"`
	}
	if err := json.Unmarshal(conf.Body.Bytes(), &doc); err != nil {
		t.Fatalf("config is not JSON: %v (%q)", err, conf.Body.String())
	}
	if !strings.HasPrefix(doc.Proxy, "https://") || !strings.HasSuffix(doc.Proxy, "@nl.example.com:2053") {
		t.Errorf("proxy = %q", doc.Proxy)
	}
	if cd := conf.Header().Get("Content-Disposition"); !strings.Contains(cd, ".conf") {
		t.Errorf("file name: %q", cd)
	}
	if png := get(pngURL, "curl/8"); png.Code != http.StatusOK || png.Header().Get("Content-Type") != "image/png" {
		t.Errorf("qr: %d %q", png.Code, png.Header().Get("Content-Type"))
	}

	// The conf call minted the user's password, so the page now carries the card and the
	// raw base64 list carries the import link — for the node, never the master.
	page := get(base, "Mozilla/5.0").Body.String()
	if !strings.Contains(page, "NaiveProxy") || !strings.Contains(page, confURL[len(base):]) {
		t.Error("page lacks the naive card")
	}
	if strings.Contains(page, "/naive/0.") {
		t.Error("page offers a master naive endpoint")
	}
	payload := get(base, "curl/8").Body.String()
	dec, err := base64.StdEncoding.DecodeString(strings.TrimSpace(payload))
	if err != nil {
		t.Fatalf("payload is not base64: %v", err)
	}
	if !strings.Contains(string(dec), "naive+https://") || !strings.Contains(string(dec), "@nl.example.com:2053") {
		t.Errorf("base64 list lacks the naive link:\n%s", dec)
	}
}
