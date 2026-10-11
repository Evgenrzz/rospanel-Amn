package nodeagent

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/naive"
	"github.com/AppsGanin/rospanel/internal/nodeapi"
)

// naiveAgent is the sampler's world on a table: a temp lane directory, a log
// to tail and the report buffers a sample folds into — the same partial
// construction the other sampler tests use.
func naiveAgent(t *testing.T) (*Agent, string) {
	t.Helper()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "access.log")
	if err := os.WriteFile(logPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	a := &Agent{
		naiveDir:  dir,
		naiveTail: newNaiveTailer(dir, logPath),
		pending:   map[int64]*nodeapi.TrafficDelta{},
		conns:     map[string]nodeapi.ConnSample{},
		sites:     map[siteKey]int64{},
		seen:      newSeenAddrs(),
	}
	return a, logPath
}

// accessLine is one caddy access-log line in the shape the lane writes:
// user_id at the top level, next to the byte counters.
func accessLine(ts int64, user, ip string, up, down int64) string {
	return fmt.Sprintf(`{"ts":%d,"request":{"client_ip":%q},"user_id":%q,"bytes_read":%d,"size":%d}`+"\n",
		ts, ip, user, up, down)
}

func appendLog(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(s); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// A sample folds the log's bytes into the same pending deltas Xray's counters
// fill, and its recent requests into connection samples. A request older than
// the online window still counts its traffic — the resume after a downtime
// replays old lines — but must not pretend the user is connected now.
func TestSampleNaiveFoldsTrafficAndSightings(t *testing.T) {
	a, logPath := naiveAgent(t)
	now := time.Now().Unix()
	appendLog(t, logPath,
		accessLine(now-30, "u7", "1.2.3.4", 100, 200)+ // fresh: counts and shows the device
			accessLine(now-30, "u7", "1.2.3.4", 50, 0)+ // same device again: deduped, bytes add up
			accessLine(now-naiveOnlineWindow-10, "u8", "5.6.7.8", 10, 20)+ // stale: traffic only
			accessLine(now-5, "", "9.9.9.9", 500, 500)) // masquerade: nobody's
	a.sampleNaive()

	d := a.pending[7]
	if d == nil || d.Up != 150 || d.Down != 200 {
		t.Fatalf("user 7 pending = %+v", d)
	}
	if d := a.pending[8]; d == nil || d.Up != 10 || d.Down != 20 {
		t.Fatalf("user 8 (a stale line) lost its traffic: %+v", d)
	}
	if len(a.pending) != 2 {
		t.Fatalf("the masquerade line was counted against someone: %+v", a.pending)
	}
	if _, ok := a.quotaActive[7]; !ok {
		t.Error("a user whose bytes moved is not marked active")
	}
	if len(a.conns) != 1 {
		t.Fatalf("conns = %+v, want the one fresh sighting", a.conns)
	}
	if c, ok := a.conns["u7\x001.2.3.4"]; !ok || c.Email != "u7" || c.IP != "1.2.3.4" {
		t.Errorf("sighting = %+v", c)
	}
	// The sample consumed the whole file: the resume point sits at its end.
	fi, err := os.Stat(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if off, _ := loadNaiveOffset(a.naiveDir); off != fi.Size() {
		t.Errorf("saved offset %d, the log is %d bytes", off, fi.Size())
	}
}

// The resume point is what makes a restart safe: saved after a sample, it
// lets the next boot pick the log up where the agent stopped, and a file
// that is garbage or absent falls back to starting at the end.
func TestNaiveOffsetPersistAndFallback(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "access.log")
	body := accessLine(time.Now().Unix(), "u1", "1.1.1.1", 1, 2)
	if err := os.WriteFile(logPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	// No offset file: a first boot must not replay a log it never accounted.
	if _, ok := loadNaiveOffset(dir); ok {
		t.Fatal("an offset came out of a directory without one")
	}
	if tr := newNaiveTailer(dir, logPath); tr.Offset() != int64(len(body)) {
		t.Errorf("first boot starts at %d, the log is %d bytes", tr.Offset(), len(body))
	}
	// A saved resume point comes back, and the tailer it builds reads what is
	// left from there.
	saveNaiveOffset(dir, 12)
	if off, ok := loadNaiveOffset(dir); !ok || off != 12 {
		t.Fatalf("loadNaiveOffset = %d, %v", off, ok)
	}
	if tr := newNaiveTailer(dir, logPath); tr.Offset() != 12 {
		t.Errorf("resume starts at %d, want 12", tr.Offset())
	}
	// Garbage in the file is a first boot again, not an error and not zero.
	if err := os.WriteFile(naiveOffsetPath(dir), []byte(`{"offset":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadNaiveOffset(dir); ok {
		t.Error("an undecodable offset was loaded")
	}
	if tr := newNaiveTailer(dir, logPath); tr.Offset() != int64(len(body)) {
		t.Errorf("after garbage the tail starts at %d, want the end", tr.Offset())
	}
}

// fakeNaiveCaddy installs a stand-in caddy-naive in dir answering the three verbs the
// supervisor uses, plus the version marker that keeps EnsureBinary from downloading the
// real release — the nodeagent-side twin of naive.supervisor_test's fake.
func fakeNaiveCaddy(t *testing.T, dir string) {
	t.Helper()
	script := "#!/bin/sh\ncase \"$1\" in\n" +
		"run) exec sleep 300;;\n" +
		"validate) exit 0;;\n" +
		"reload) exit 0;;\n" +
		"esac\nexit 0\n"
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, naive.BinaryName)
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin+".version", []byte(naive.PinnedVersion+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// D2 cert handoff: the panel does not know a node's data directory, so it sends the cert
// sentinels and the runner substitutes the same cert/key pair the Xray config gets; a
// literal path is an operator override and is used exactly as sent. The proof is the
// Caddyfile the supervisor renders and validates before it spawns — its `tls` line must
// carry the substituted pair, and auto_https must stay off (the node's TLS front owns
// 80/443, so caddy-naive never requests a certificate of its own).
func TestSyncNaiveCertHandoff(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the lane is linux-only (see naive.EnsureBinary)")
	}
	dir := t.TempDir()
	fakeNaiveCaddy(t, dir)
	panelCert := filepath.Join(dir, "certs", "cert.pem")
	panelKey := filepath.Join(dir, "certs", "key.pem")
	a := &Agent{
		naiveDir: dir,
		naiveSup: naive.NewSupervisor(dir),
		certPath: panelCert,
		keyPath:  panelKey,
	}
	t.Cleanup(a.naiveSup.Stop)

	// rendered returns the whole Caddyfile the supervisor wrote on the last Apply.
	rendered := func() string {
		t.Helper()
		cf, err := os.ReadFile(filepath.Join(dir, "Caddyfile"))
		if err != nil {
			t.Fatal(err)
		}
		return string(cf)
	}
	tlsLine := func() string {
		t.Helper()
		for _, line := range strings.Split(rendered(), "\n") {
			if s := strings.TrimSpace(line); strings.HasPrefix(s, "tls ") {
				return s
			}
		}
		t.Fatalf("no tls line in the rendered Caddyfile:\n%s", rendered())
		return ""
	}

	// The sentinels: the runner hands caddy the panel's own cert, not the placeholder.
	a.syncNaive(&nodeapi.NaiveState{
		Port:     2053,
		CertPath: nodeapi.CertPathSentinel,
		KeyPath:  nodeapi.KeyPathSentinel,
		Users:    []nodeapi.NaiveUser{{Name: "u7", Pass: "0123456789abcdef01234567"}},
	}, "vpn.example.com")
	if a.naiveErr != "" {
		t.Fatalf("the sentinel apply reported an error: %q", a.naiveErr)
	}
	if got, want := tlsLine(), "tls "+panelCert+" "+panelKey; got != want {
		t.Errorf("sentinel handoff = %q, want %q", got, want)
	}
	if !strings.Contains(rendered(), "auto_https off") {
		t.Error("caddy-naive would request its own certificate: auto_https is not off")
	}

	// A literal path is an operator override and is used exactly as sent.
	a.syncNaive(&nodeapi.NaiveState{
		Port:     2053,
		CertPath: "/opt/custom/fullchain.pem",
		KeyPath:  "/opt/custom/privkey.pem",
		Users:    []nodeapi.NaiveUser{{Name: "u7", Pass: "0123456789abcdef01234567"}},
	}, "vpn.example.com")
	if a.naiveErr != "" {
		t.Fatalf("the override apply reported an error: %q", a.naiveErr)
	}
	if got, want := tlsLine(), "tls /opt/custom/fullchain.pem /opt/custom/privkey.pem"; got != want {
		t.Errorf("literal override = %q, want %q", got, want)
	}
}
