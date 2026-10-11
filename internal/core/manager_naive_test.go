package core

import (
	"slices"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
)

// Building a node's naive state mints a password for every working user allowed on the
// lane, persists it, and reuses it on the next build — the credential a client's link
// carries must not rotate under them because another node synced. The port and display
// name ride on the node's connections blob; the probe secret and masquerade URL are the
// GLOBAL defaults every node inherits. Off on the node, or with no port, means no state.
func TestNodeNaiveStateClaimsCredentials(t *testing.T) {
	t.Parallel()
	m := bulkTestManager(t)
	ctx := adminCtx()
	a, _ := m.CreateUser(ctx, "a", 0, 0)
	b, _ := m.CreateUser(ctx, "b", 0, 0)
	n, err := m.CreateNode("nl", "nl.example.com")
	if err != nil {
		t.Fatal(err)
	}

	// The global defaults a node inherits, set once for the whole panel.
	if err := m.store.SetNaiveConfig(naiveDefaultPort, "hidden.example", "https://cover.example", "Naive"); err != nil {
		t.Fatal(err)
	}
	// The node runs the lane: its own toggle, and its own port + name in the blob.
	if err := m.store.SetNodeNaiveEnabled(n.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := m.store.SetNodeConnections(n.ID, &model.NodeConnections{NaivePort: naiveDefaultPort, NaiveName: "Naive NL"}); err != nil {
		t.Fatal(err)
	}

	set, _ := m.store.GetSettings()
	node, _ := m.store.GetNode(n.ID)
	ns := nodeSettings(set, node)
	if !ns.NaiveEnabled || ns.NaivePort != naiveDefaultPort {
		t.Fatalf("node settings: on=%v port=%d, want on and %d", ns.NaiveEnabled, ns.NaivePort, naiveDefaultPort)
	}
	// The probe secret and masquerade URL are the global ones, carried through the copy.
	if ns.NaiveProbeSecret != "hidden.example" || ns.NaiveMasqueradeURL != "https://cover.example" {
		t.Fatalf("node did not inherit the global defaults: probe=%q masq=%q", ns.NaiveProbeSecret, ns.NaiveMasqueradeURL)
	}

	users, _ := m.store.WorkingCredentials(1)
	st := m.nodeNaiveState(node, ns, users, nil)
	if st == nil {
		t.Fatal("no naive state for a node with the lane on")
	}
	if st.Port != naiveDefaultPort || st.ProbeSecret != "hidden.example" || st.MasqueradeURL != "https://cover.example" {
		t.Errorf("state: port=%d probe=%q masq=%q, want the node port and the global defaults",
			st.Port, st.ProbeSecret, st.MasqueradeURL)
	}
	if len(st.Users) != 2 {
		t.Fatalf("state users: %+v, want both a and b", st.Users)
	}
	// Each user is u<id> with a minted password, and that password was persisted — the
	// node serves what the panel stored, so a later build reads the same credential.
	byName := map[string]string{}
	for _, u := range st.Users {
		byName[u.Name] = u.Pass
	}
	for _, id := range []int64{a.ID, b.ID} {
		pass := byName[model.UserEmail(id)]
		if pass == "" {
			t.Errorf("user %d has no naive credential in the state", id)
			continue
		}
		if stored, _ := m.store.GetUser(id); stored.NaivePass != pass {
			t.Errorf("user %d: state pass %q was not persisted (store has %q)", id, pass, stored.NaivePass)
		}
	}

	// A second build reuses the same passwords: the claim mints once, so the credential
	// is stable across syncs and the links already handed out keep working.
	users2, _ := m.store.WorkingCredentials(1)
	for _, u := range m.nodeNaiveState(node, ns, users2, nil).Users {
		if byName[u.Name] != u.Pass {
			t.Errorf("user %s: password changed between builds (%q then %q)", u.Name, byName[u.Name], u.Pass)
		}
	}

	// Off on the node ⇒ no state, even with the global lane defaults set.
	off := *ns
	off.NaiveEnabled = false
	if st := m.nodeNaiveState(node, &off, users, nil); st != nil {
		t.Error("state produced for a node with the lane off")
	}
	// No port ⇒ no state either: the guard is on both, so a half-configured node does
	// not render a Caddyfile listening on :0.
	noPort := *ns
	noPort.NaivePort = 0
	if st := m.nodeNaiveState(node, &noPort, users, nil); st != nil {
		t.Error("state produced for a node with no naive port")
	}
}

// The naive lane obeys the same per-node grant as every other built-in lane: a user
// allowed on the node for another protocol but not for naive gets no credential, and a
// nil access map means "no restriction" so every working user is served.
func TestNodeNaiveStateFiltersByGrant(t *testing.T) {
	t.Parallel()
	m := bulkTestManager(t)
	ctx := adminCtx()
	u, _ := m.CreateUser(ctx, "u", 0, 0)
	n, err := m.CreateNode("nl", "nl.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.store.SetNodeNaiveEnabled(n.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := m.store.SetNodeConnections(n.ID, &model.NodeConnections{NaivePort: naiveDefaultPort}); err != nil {
		t.Fatal(err)
	}
	set, _ := m.store.GetSettings()
	node, _ := m.store.GetNode(n.ID)
	ns := nodeSettings(set, node)
	users, _ := m.store.WorkingCredentials(1)

	// A grant for another lane on this node leaves the user off the naive one.
	other := map[int64]model.Access{u.ID: {Tokens: map[string]bool{model.BuiltinToken(n.ID, model.LaneVLESS): true}}}
	if st := m.nodeNaiveState(node, ns, users, other); st == nil || len(st.Users) != 0 {
		t.Fatalf("a user without the naive grant got a credential: %+v", st)
	}

	// Adding the naive token for this node lets them in, as u<id>.
	granted := map[int64]model.Access{u.ID: {Tokens: map[string]bool{
		model.BuiltinToken(n.ID, model.LaneVLESS): true,
		model.BuiltinToken(n.ID, model.LaneNaive): true,
	}}}
	if st := m.nodeNaiveState(node, ns, users, granted); st == nil || len(st.Users) != 1 || st.Users[0].Name != model.UserEmail(u.ID) {
		t.Fatalf("a user with the naive grant was left out: %+v", st)
	}

	// A nil access map restricts nothing: every working user is served.
	if st := m.nodeNaiveState(node, ns, users, nil); st == nil || len(st.Users) != 1 {
		t.Fatalf("a nil access map restricted the lane: %+v", st)
	}
}

// Regenerating a naive password touches the naive password and nothing else: the UUID
// (VLESS/REALITY), the password (Trojan) and the AmneziaWG key a user arrived with all
// survive. A single regen mints the user's first password; a fleet-wide regen rotates
// exactly the users who already had one and leaves an unclaimed user empty.
func TestRegenNaivePassLeavesOtherLanesIntact(t *testing.T) {
	t.Parallel()
	m := bulkTestManager(t)
	// Two users imported with an AmneziaWG key of their own — a different lane's secret
	// a naive regeneration must not disturb.
	mk := func(name, wg string) int64 {
		t.Helper()
		u, err := m.store.ImportUser(store.ImportedUser{
			Name: name, UUID: "uuid-" + name, Password: "pw-" + name,
			SubToken: "tok-" + name, Enabled: true, WGPrivateKey: wg,
		})
		if err != nil {
			t.Fatal(err)
		}
		return u.ID
	}
	a, b := mk("a", "wg-key-a"), mk("b", "wg-key-b")

	// Nobody has a naive password yet, so a fleet-wide regen rotates nobody.
	if cnt, err := m.RegenAllNaivePass(); err != nil || cnt != 0 {
		t.Fatalf("RegenAllNaivePass on an unclaimed store = (%d, %v), want (0, nil)", cnt, err)
	}

	// A single regen mints a's first password and leaves every other lane alone.
	if err := m.RegenNaivePass(a); err != nil {
		t.Fatal(err)
	}
	after, err := m.store.GetUser(a)
	if err != nil {
		t.Fatal(err)
	}
	if after.NaivePass == "" {
		t.Error("RegenNaivePass did not mint a password")
	}
	if after.WGPrivateKey != "wg-key-a" || after.Password != "pw-a" || after.UUID != "uuid-a" {
		t.Errorf("RegenNaivePass touched another lane: wg=%q pw=%q uuid=%q",
			after.WGPrivateKey, after.Password, after.UUID)
	}

	// Only a is claimed, so the fleet-wide set is exactly [a] — the users the lane was
	// ever enabled for, and nobody else.
	if ids, err := m.store.NaivePassUserIDs(); err != nil || !slices.Equal(ids, []int64{a}) {
		t.Fatalf("NaivePassUserIDs = (%v, %v), want [%d]", ids, err, a)
	}
	first := after.NaivePass
	if cnt, err := m.RegenAllNaivePass(); err != nil || cnt != 1 {
		t.Fatalf("RegenAllNaivePass = (%d, %v), want (1, nil)", cnt, err)
	}
	againA, _ := m.store.GetUser(a)
	if againA.NaivePass == "" || againA.NaivePass == first {
		t.Errorf("a's password was not rotated: %q then %q", first, againA.NaivePass)
	}
	if againA.WGPrivateKey != "wg-key-a" {
		t.Errorf("the fleet-wide regen touched a's AmneziaWG key: %q", againA.WGPrivateKey)
	}
	// b was never claimed: the bulk regen leaves them empty rather than enrolling them
	// in a lane that was never turned on for them.
	afterB, _ := m.store.GetUser(b)
	if afterB.NaivePass != "" {
		t.Errorf("b was never claimed but the bulk regen gave them a password: %q", afterB.NaivePass)
	}
	if afterB.WGPrivateKey != "wg-key-b" || afterB.Password != "pw-b" {
		t.Errorf("the bulk regen touched b's other lanes: wg=%q pw=%q", afterB.WGPrivateKey, afterB.Password)
	}
}
