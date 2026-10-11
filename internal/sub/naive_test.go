package sub

import (
	"encoding/base64"
	"encoding/json"
	"html"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
)

// naiveNodeSet is a remote node's materialised settings with the NaiveProxy lane on: its
// own toggle, port and display name (the probe secret and masquerade URL are the global
// defaults a node inherits and do not reach a share link).
func naiveNodeSet(id int64) *model.Settings {
	return &model.Settings{
		Host: "nl.example.com", SNI: "nl.example.com", ServerID: id,
		NaiveEnabled: true, NaivePort: 2053, NaiveName: "Naive NL",
	}
}

func anyNaive(links []string) bool {
	for _, l := range links {
		if strings.HasPrefix(l, "naive+https://") {
			return true
		}
	}
	return false
}

// The naive link is handed out only where the lane actually runs — a node, never the
// master — and only to a user who has a password and whose access grants this node's
// naive lane. Every clause is a way a credential could otherwise leak to someone who
// cannot use it, or point at a server that is not listening.
func TestNaiveLinkFollowsAccessAndIsNodeOnly(t *testing.T) {
	t.Parallel()
	u := model.User{ID: 7, UUID: "u", Password: "p", NaivePass: "secret123"}
	node := Server{Set: naiveNodeSet(5), Access: model.UnrestrictedAccess()}

	const want = "naive+https://u7:secret123@nl.example.com:2053#Naive%20NL"
	links := ShareLinks(u, node)
	var naive []string
	for _, l := range links {
		if strings.HasPrefix(l, "naive+https://") {
			naive = append(naive, l)
		}
	}
	if len(naive) != 1 || naive[0] != want {
		t.Fatalf("naive links = %v, want [%s]", naive, want)
	}

	// The master never yields a naive link, even with the lane flagged on: it does not
	// run caddy-naive, so a link to it would dial a closed port.
	master := Server{Set: naiveNodeSet(model.LocalNodeID), Access: model.UnrestrictedAccess()}
	if ls := ShareLinks(u, master); anyNaive(ls) {
		t.Errorf("the master handed out a naive link: %v", ls)
	}

	// A group that grants nothing: no link. The naive token for THIS node brings it back.
	denied := node
	denied.Access = model.Access{Tokens: map[string]bool{}}
	if ls := ShareLinks(u, denied); anyNaive(ls) {
		t.Errorf("a user with no grants got a naive link: %v", ls)
	}
	granted := node
	granted.Access = model.Access{Tokens: map[string]bool{model.BuiltinToken(5, model.LaneNaive): true}}
	if ls := ShareLinks(u, granted); !anyNaive(ls) {
		t.Errorf("the naive grant did not bring the link back: %v", ls)
	}
	// A grant for a DIFFERENT node does not: the token is keyed by server.
	elsewhere := node
	elsewhere.Access = model.Access{Tokens: map[string]bool{model.BuiltinToken(6, model.LaneNaive): true}}
	if ls := ShareLinks(u, elsewhere); anyNaive(ls) {
		t.Errorf("a grant for another node leaked a naive link: %v", ls)
	}

	// No password minted yet: nothing to hand out.
	noPass := u
	noPass.NaivePass = ""
	if ls := ShareLinks(noPass, node); anyNaive(ls) {
		t.Errorf("a user with no naive password got a link: %v", ls)
	}
}

// The naive link rides the universal base64 payload like every other lane, so a client
// that reads the link list imports it — and exactly one entry appears across the master
// plus the node, never a duplicate from the master.
func TestNaiveLinkRidesBase64Payload(t *testing.T) {
	t.Parallel()
	u := model.User{ID: 7, NaivePass: "secret123"}
	servers := []Server{
		{Set: &model.Settings{Host: "m.example.com", ServerID: model.LocalNodeID}, Access: model.UnrestrictedAccess()},
		{Set: naiveNodeSet(5), Access: model.UnrestrictedAccess()},
	}
	dec, err := base64.StdEncoding.DecodeString(Base64Payload(ShareLinksAll(u, servers)))
	if err != nil {
		t.Fatalf("payload is not base64: %v", err)
	}
	if n := strings.Count(string(dec), "naive+https://"); n != 1 {
		t.Errorf("the list carries %d naive links, want 1:\n%s", n, dec)
	}
	if !strings.Contains(string(dec), "naive+https://u7:secret123@nl.example.com:2053") {
		t.Errorf("the base64 list lacks the naive link:\n%s", dec)
	}
}

// The page offers a NaiveProxy card — localised, with the import link and the node's
// config download — and no card for the master.
func TestNaiveCardOnPage(t *testing.T) {
	t.Parallel()
	u := model.User{Name: "Alice", SubToken: "tok", ID: 7, NaivePass: "secret123"}
	local := &model.Settings{Host: "m.example.com", SubPath: "sub", ServerID: model.LocalNodeID}
	servers := []Server{
		{Set: local, Access: model.UnrestrictedAccess()},
		{Set: naiveNodeSet(5), Access: model.UnrestrictedAccess()},
	}
	for _, lang := range []i18n.Lang{i18n.EN, i18n.RU} {
		page, err := Page(u, local, servers, Billing{}, Devices{}, Access{}, true, lang)
		if err != nil {
			t.Fatalf("render %s: %v", lang, err)
		}
		s := html.UnescapeString(string(page))
		if !strings.Contains(s, "NaiveProxy") {
			t.Errorf("%s page has no NaiveProxy card", lang)
		}
		if !strings.Contains(s, "naive+https://u7:secret123@nl.example.com:2053") {
			t.Errorf("%s page does not carry the import link", lang)
		}
		if !strings.Contains(s, "/naive/5.conf") || !strings.Contains(s, "/naive/5.png") {
			t.Errorf("%s page has no config download / QR for the node", lang)
		}
		if strings.Contains(s, "/naive/0.") {
			t.Errorf("%s page offers a master naive endpoint", lang)
		}
	}
	// The card is worded in the page's language: the Russian hint must actually render.
	ru, err := Page(u, local, servers, Billing{}, Devices{}, Access{}, true, i18n.RU)
	if err != nil {
		t.Fatalf("render ru: %v", err)
	}
	if !strings.Contains(string(ru), "отсканируйте QR") {
		t.Error("the Russian naive hint did not render")
	}
	en, err := Page(u, local, servers, Billing{}, Devices{}, Access{}, true, i18n.EN)
	if err != nil {
		t.Fatalf("render en: %v", err)
	}
	if !strings.Contains(string(en), "scan the QR") {
		t.Error("the English naive hint did not render")
	}
}

// The client config is the {"proxy":"https://…"} JSON the naiveproxy client reads,
// carrying the same credential as the link; a user with no password gets nothing.
func TestNaiveClientConf(t *testing.T) {
	t.Parallel()
	set := naiveNodeSet(5)
	var doc struct {
		Proxy string `json:"proxy"`
	}
	conf := NaiveClientConf(model.User{ID: 7, NaivePass: "secret123"}, set)
	if err := json.Unmarshal([]byte(conf), &doc); err != nil {
		t.Fatalf("client config is not JSON: %v (%q)", err, conf)
	}
	if doc.Proxy != "https://u7:secret123@nl.example.com:2053" {
		t.Errorf("proxy = %q", doc.Proxy)
	}
	if c := NaiveClientConf(model.User{ID: 7}, set); c != "" {
		t.Errorf("a user with no password got a config: %q", c)
	}
}
