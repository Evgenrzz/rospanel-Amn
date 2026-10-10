package nodeagent

import (
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/AppsGanin/rospanel/internal/naive"
	"github.com/AppsGanin/rospanel/internal/nodeapi"
)

// The node's NaiveProxy lane: a caddy-naive child process supervised like the
// opera-proxy helper (see internal/naive.Supervisor), brought in line with what
// the panel sent on every state apply, and read for traffic on every stats
// sample. The access log is the lane's only counter — there is no API to poll —
// so the sampler tails it and folds entries into the same report fields Xray's
// users fill: the panel never learns which lane a byte came through.

// naiveOnlineWindow mirrors core.awgOnlineWindow: a request this recent means
// the user is connected now. Entries can be older — the offset resume below
// replays whatever was logged while the agent was down — so the window is
// checked per entry instead of trusting the sample's freshness.
const naiveOnlineWindow = 180

// naiveErrMax truncates a reported failure, for the same reason awgErrMax
// exists: it is remote input that ends up in an admin's chat.
const naiveErrMax = 200

// syncNaive applies (or tears down) the lane for the state the panel sent.
// The domain is the node's host from the same meta: the Caddyfile lists it
// next to the bare port so the CONNECT matches however the client sets SNI.
func (a *Agent) syncNaive(st *nodeapi.NaiveState, host string) {
	if a.naiveSup == nil { // an Agent built without the lane's fields (tests)
		return
	}
	if st == nil {
		a.naiveSup.Stop()
		a.setNaiveError("")
		return
	}
	// The panel sends the cert sentinels (it does not know this node's data
	// directory); substitute the same pair the Xray config gets. A literal
	// path is an operator override and is used as sent.
	certFile, keyFile := a.certPath, a.keyPath
	if st.CertPath != "" && st.CertPath != nodeapi.CertPathSentinel {
		certFile = st.CertPath
	}
	if st.KeyPath != "" && st.KeyPath != nodeapi.KeyPathSentinel {
		keyFile = st.KeyPath
	}
	cfg := naive.Config{
		Port:        st.Port,
		Domain:      host,
		CertFile:    certFile,
		KeyFile:     keyFile,
		ProbeSecret: st.ProbeSecret,
	}
	for _, u := range st.Users {
		cfg.Users = append(cfg.Users, naive.User{Name: u.Name, Pass: u.Pass})
	}
	if st.MasqueradeURL != "" {
		cfg.Masq = naive.Masquerade{Kind: naive.MasqMirror, URL: st.MasqueradeURL}
	}
	err := a.naiveSup.Apply(cfg)
	switch {
	case err == nil:
		a.setNaiveError("")
	case errors.Is(err, naive.ErrUnsupported):
		// Not a transient failure: this machine cannot run the server at all.
		// Worth reporting rather than swallowing — the operator switched the
		// lane on for a node where it will never come up.
		a.setNaiveError("naiveproxy is not supported on this machine")
	default:
		slog.Warn("node: naiveproxy apply failed", "err", err)
		a.setNaiveError(err.Error())
	}
}

// setNaiveError records the last apply outcome for the next sync. Bounded: it
// is an error string from a remote machine on its way into a chat message.
func (a *Agent) setNaiveError(msg string) {
	if len(msg) > naiveErrMax {
		msg = msg[:naiveErrMax]
	}
	a.naiveMu.Lock()
	a.naiveErr = msg
	a.naiveMu.Unlock()
}

// naiveState is what the sync reports: whether the server is up, and why not.
// Nil-safe on the supervisor, for the same reason seenAddrs.note is: an Agent
// built without the lane's fields (tests, partial construction) must still
// produce a sync request.
func (a *Agent) naiveState() (bool, string) {
	a.naiveMu.Lock()
	msg := a.naiveErr
	a.naiveMu.Unlock()
	if a.naiveSup == nil {
		return false, msg
	}
	return a.naiveSup.Running(), msg
}

// sampleNaive folds the access log's new entries into the pending traffic
// deltas and its recent requests into the connection samples, exactly as
// sampleAWG does for the tunnel. Single-owner: only the stats loop calls it,
// which is what lets the tailer stay lock-free.
func (a *Agent) sampleNaive() {
	if a.naiveTail == nil { // an Agent built without the lane's fields (tests)
		return
	}
	entries, err := a.naiveTail.Poll()
	if err != nil {
		slog.Warn("node: naiveproxy access log", "err", err)
		return
	}
	if len(entries) > 0 {
		now := time.Now().Unix()
		type sighting struct{ email, ip string }
		var seen []sighting
		a.statsMu.Lock()
		for _, e := range entries {
			uid, ok := userIDFromEmail(e.UserID)
			if !ok {
				continue
			}
			if e.Up > 0 || e.Down > 0 {
				d := a.pending[uid]
				if d == nil {
					d = &nodeapi.TrafficDelta{UserID: uid}
					a.pending[uid] = d
				}
				if e.Up > 0 {
					d.Up += e.Up
				}
				if e.Down > 0 {
					d.Down += e.Down
				}
				if a.quotaActive == nil {
					a.quotaActive = map[int64]time.Time{}
				}
				a.quotaActive[uid] = time.Now()
			}
			if e.ClientIP != "" && now-e.TS <= naiveOnlineWindow {
				seen = append(seen, sighting{e.UserID, e.ClientIP})
			}
		}
		a.statsMu.Unlock()
		for _, s := range seen {
			a.recordConn(s.email, s.ip, "")
		}
	}
	// Persist where the tail got to, so an agent restart resumes here instead
	// of skipping to the end (traffic logged while it was down is the users'
	// and must still be counted). Best-effort: losing the file only undercounts
	// the downtime window, which is the same trade a missing sample makes.
	saveNaiveOffset(a.naiveDir, a.naiveTail.Offset())
}

// naiveOffsetPath is the sampler's resume point, next to the log it tails.
func naiveOffsetPath(dir string) string { return filepath.Join(dir, "tail.json") }

// saveNaiveOffset persists the tail offset atomically (write-temp + rename),
// so a crash mid-write cannot leave a number that sends the next boot's tail
// to a wrong place in the file.
func saveNaiveOffset(dir string, offset int64) {
	b, err := json.Marshal(struct {
		Offset int64 `json:"offset"`
	}{Offset: offset})
	if err != nil {
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	tmp := naiveOffsetPath(dir) + ".new"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return
	}
	if err := os.Rename(tmp, naiveOffsetPath(dir)); err != nil {
		os.Remove(tmp)
	}
}

// loadNaiveOffset reads the persisted offset. ok is false when there is none —
// a first boot, which must start at the END of any log it finds (that log
// belongs to a deployment this agent never accounted for).
func loadNaiveOffset(dir string) (int64, bool) {
	b, err := os.ReadFile(naiveOffsetPath(dir))
	if err != nil {
		return 0, false
	}
	var v struct {
		Offset int64 `json:"offset"`
	}
	if err := json.Unmarshal(b, &v); err != nil || v.Offset < 0 {
		return 0, false
	}
	return v.Offset, true
}

// newNaiveTailer is the boot-time tail: resume from the persisted offset, or
// start at the end of whatever log is already there.
func newNaiveTailer(dir, logPath string) *naive.Tailer {
	if off, ok := loadNaiveOffset(dir); ok {
		return naive.NewTailerAt(logPath, off)
	}
	return naive.NewTailer(logPath)
}
