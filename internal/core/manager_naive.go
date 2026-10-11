package core

import (
	"fmt"
	"strings"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/naive"
	"github.com/AppsGanin/rospanel/internal/nodeapi"
	"github.com/AppsGanin/rospanel/internal/store"
)

// The NaiveProxy lane. Unlike AmneziaWG the master never runs it — NaiveProxy is a
// node-only lane — so the panel's job here is the two things only it can do: own the
// per-user credentials (mint a password the first time the lane is enabled for a user,
// regenerate on demand) and build each node's nodeapi.NaiveState from the global
// defaults every node inherits and the node's own port. The node runs the server
// (internal/nodeagent) and folds its access log into the same traffic and sighting
// fields Xray's users fill, so the panel never learns which lane a byte came through.
//
// There is deliberately no syncNaiveLocked/PollNaive/naive supervisor here: those are
// the master-side runtime the AmneziaWG lane has and this one does not. Nothing on the
// master binds the naive port, so nothing here checks whether it is free — that is the
// node's own concern, and the Caddyfile it renders is the backstop, exactly as a node's
// local `xray -test` is for the other lanes.

// naiveDefaultPort is the TCP port a freshly-enabled lane listens on. The panel fronts
// 443/80 with its own TLS, so the lane takes a high port; 2053 is the conventional
// NaiveProxy choice (see internal/naive.Config.Port) and, unlike the AmneziaWG port, it
// is stable rather than random: it is embedded in every naive+https:// link a client
// holds, so rotating it would invalidate them all.
const naiveDefaultPort = 2053

// claimNaive gives the users who lack one their NaiveProxy password and records it on
// the users handed in. Like the AmneziaWG identity it is made once and never rotated on
// its own — a regeneration goes through RegenNaivePass, which overwrites deliberately.
//
// One store transaction for the lot: the first node to bring the lane up claims for
// every user it has, and two nodes building their state at the same moment must not
// leave one holding a password the user's link no longer carries.
func (m *Manager) claimNaive(users []*model.User) error {
	var claims []store.NaiveClaim
	for _, u := range users {
		if u.NaivePass != "" {
			continue
		}
		pass, err := naive.GeneratePassword()
		if err != nil {
			return err
		}
		claims = append(claims, store.NaiveClaim{UserID: u.ID, Pass: pass})
	}
	if len(claims) == 0 {
		return nil
	}
	// Another node may have claimed first; what the store kept is what counts.
	got, err := m.store.ClaimUsersNaive(claims)
	if err != nil {
		return err
	}
	for _, u := range users {
		if pass, ok := got[u.ID]; ok {
			u.NaivePass = pass
		}
	}
	// The users every node's state is built from were read before these existed.
	m.dropNodeInputs()
	return nil
}

// naiveUsersClaimed turns the working users allowed on a server's naive lane into
// credential pairs, claiming a password for anyone who lacks one. claimed is false when
// a claim failed, so the caller leaves the node's state incomplete and it is retried on
// the next sync rather than served without those users.
//
// The username is model.UserEmail(id) — the same "u<id>" form the Xray email and the
// node's access-log reader use — so a naive byte folds into the user's existing counters
// and sightings without the panel ever naming the lane.
func (m *Manager) naiveUsersClaimed(serverID int64, users []model.User, access map[int64]model.Access) ([]nodeapi.NaiveUser, bool) {
	allowed := make([]*model.User, 0, len(users))
	for i := range users {
		if model.AccessOf(access, users[i].ID).AllowsBuiltin(serverID, model.LaneNaive) {
			allowed = append(allowed, &users[i])
		}
	}
	claimed := true
	if err := m.claimNaive(allowed); err != nil {
		logErr("naive: user credentials", "server", serverID, "err", err)
		claimed = false
	}
	out := make([]nodeapi.NaiveUser, 0, len(allowed))
	for _, u := range allowed {
		if u.NaivePass == "" {
			// Left out rather than served with an empty password: caddy would read
			// that as a credential matching nothing, so the user could not connect
			// and would not know why. A claim that could not mint, or a password
			// that will not decrypt (a wrong or missing secrets.key), lands here.
			logErr("naive: user password unreadable (wrong or missing secrets.key?)", "user", u.ID)
			continue
		}
		out = append(out, nodeapi.NaiveUser{Name: model.UserEmail(u.ID), Pass: u.NaivePass})
	}
	return out, claimed
}

// nodeNaiveState is what a node needs to run its NaiveProxy lane: the port, the TLS
// sentinels the agent rewrites to its own paths, the global probe secret and masquerade
// URL, and the credentials of every user allowed on it. nil when the lane is off there.
func (m *Manager) nodeNaiveState(n *model.Node, ns *model.Settings, users []model.User, access map[int64]model.Access) *nodeapi.NaiveState {
	st, _ := m.nodeNaiveStateClaimed(n, ns, users, access)
	return st
}

// nodeNaiveStateClaimed is nodeNaiveState that also says whether every allowed user's
// password was claimed (true when there is no lane to claim for).
func (m *Manager) nodeNaiveStateClaimed(n *model.Node, ns *model.Settings, users []model.User, access map[int64]model.Access) (*nodeapi.NaiveState, bool) {
	if !ns.NaiveEnabled || ns.NaivePort == 0 {
		return nil, true
	}
	// claimNaive records a password it mints on the user it was handed, and these users
	// are the snapshot every node shares (nodeInputs): it works on its own copy.
	creds, claimed := m.naiveUsersClaimed(n.ID, append([]model.User(nil), users...), access)
	// The cert paths are the sentinels generateNodeState put on ns: the panel does not
	// know this node's data dir, so the agent substitutes its own absolute paths and the
	// hashed state stays stable across nodes.
	return &nodeapi.NaiveState{
		Port:          ns.NaivePort,
		CertPath:      ns.CertPath,
		KeyPath:       ns.KeyPath,
		ProbeSecret:   ns.NaiveProbeSecret,
		MasqueradeURL: ns.NaiveMasqueradeURL,
		Users:         creds,
	}, claimed
}

// validateNaiveUpdate checks the port, probe secret and masquerade URL an operator
// entered for the lane and returns them normalised: 0 for the port means "use the
// default". The port is range-checked here and held against the custom inbounds (see
// reservedPorts); whether it is actually free on a given node is that node's concern,
// and its rendered Caddyfile is the backstop for the rest.
func validateNaiveUpdate(port int, probeSecret, masqueradeURL string) (int, string, string, error) {
	if port < 0 || port > 65535 {
		return 0, "", "", invalidCode("err.naivePortRange", "порт NaiveProxy вне диапазона 1–65535")
	}
	probeSecret = strings.TrimSpace(probeSecret)
	if !naive.ValidProbeSecret(probeSecret) {
		return 0, "", "", invalidCode("err.naiveProbeSecret", "секрет probe_resistance: 1–63 символа, буквы, цифры, точка, _ и -")
	}
	masqueradeURL = strings.TrimSpace(masqueradeURL)
	if err := naive.ValidMasqueradeURL(masqueradeURL); err != nil {
		return 0, "", "", invalidCode("err.naiveMasquerade", "URL маскировки: http(s)://домен или 127.0.0.1")
	}
	return port, probeSecret, masqueradeURL, nil
}

// RegenNaivePass gives one user a fresh NaiveProxy password, invalidating every
// naive+https:// link handed out for them so far — which is the point of asking for it.
// It wakes the nodes so the new credential reaches every lane the user is on without
// waiting for a cadence tick. A user the lane was never enabled for simply gets their
// first password; the credential is only ever served where the lane is on and the user
// is allowed on it.
func (m *Manager) RegenNaivePass(userID int64) error {
	pass, err := naive.GeneratePassword()
	if err != nil {
		return err
	}
	if err := m.store.SetUsersNaivePass(map[int64]string{userID: pass}); err != nil {
		return err
	}
	m.dropNodeInputs()
	m.notifyNodes()
	return nil
}

// RegenAllNaivePass re-mints the password of every user the lane was ever enabled for
// (those who already have one), invalidating every link in one stroke — the recovery
// move after a suspected credential leak. It returns how many users were rotated and
// wakes the nodes. Users without a password are left alone: the lane mints one when it
// is first enabled for them, so a bulk regen rotates exactly the live credential set.
func (m *Manager) RegenAllNaivePass() (int, error) {
	ids, err := m.store.NaivePassUserIDs()
	if err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	passes := make(map[int64]string, len(ids))
	for _, id := range ids {
		pass, err := naive.GeneratePassword()
		if err != nil {
			return 0, err
		}
		passes[id] = pass
	}
	if err := m.store.SetUsersNaivePass(passes); err != nil {
		return 0, err
	}
	m.dropNodeInputs()
	m.notifyNodes()
	return len(passes), nil
}

// NaiveClientConfig renders one user's NaiveProxy client config for one node — the
// {"proxy":"https://u<id>:pass@host:port"} file the naiveproxy client and its forks
// import. s is that node's materialised settings (from nodeSettings). It claims a password
// for a user who lacks one, so a freshly-enabled lane serves a working config on the very
// first download rather than an empty one — the same on-demand claim AWGClientConfig makes
// for a tunnel key. Fails when the lane is off, the server is the master (naive is
// node-only and never runs there), or no password could be minted.
func (m *Manager) NaiveClientConfig(u *model.User, s *model.Settings) (string, error) {
	if s.ServerID == model.LocalNodeID || !s.NaiveEnabled || s.NaivePort == 0 {
		return "", fmt.Errorf("naive: lane is off on server %d", s.ServerID)
	}
	if err := m.claimNaive([]*model.User{u}); err != nil {
		return "", err
	}
	if u.NaivePass == "" {
		return "", fmt.Errorf("naive: no password for user %d", u.ID)
	}
	return naive.ClientConfig(naive.UserName(u.ID), u.NaivePass, s.Host, s.NaivePort), nil
}
