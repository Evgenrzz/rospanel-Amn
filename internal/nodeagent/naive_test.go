package nodeagent

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

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

// accessLine is one caddy access-log line in the shape the lane parses.
func accessLine(ts int64, user, ip string, up, down int64) string {
	return fmt.Sprintf(`{"ts":%d,"bytes_read":%d,"size":%d,"request":{"user_id":%q,"client_ip":%q}}`+"\n",
		ts, up, down, user, ip)
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
