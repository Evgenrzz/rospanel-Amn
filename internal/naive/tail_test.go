package naive

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func accessLine(user string, up, down int) string {
	return `{"ts":1760000000,"request":{"client_ip":"1.2.3.4","user_id":"` + user +
		`"},"bytes_read":` + strconv.Itoa(up) + `,"size":` + strconv.Itoa(down) + "}\n"
}

func writeLog(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A fresh tailer starts at the END of an existing log: history predates the
// lane coming up under this panel, and replaying it would double-charge on
// every agent restart.
func TestTailerSkipsHistory(t *testing.T) {
	t.Parallel()
	p := filepath.Join(t.TempDir(), "access.log")
	writeLog(t, p, accessLine("u1", 10, 20))

	tr := NewTailer(p)
	got, err := tr.Poll()
	if err != nil || len(got) != 0 {
		t.Fatalf("history replayed: %d entries, err %v", len(got), err)
	}
}

func TestTailerGrowthAndPartial(t *testing.T) {
	t.Parallel()
	p := filepath.Join(t.TempDir(), "access.log")
	writeLog(t, p, "")
	tr := NewTailer(p)

	// Two complete lines, then a third split mid-write.
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(accessLine("u1", 10, 20) + accessLine("u2", 30, 40)); err != nil {
		t.Fatal(err)
	}
	got, err := tr.Poll()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].UserID != "u1" || got[1].Down != 40 {
		t.Fatalf("complete lines: %+v", got)
	}

	if _, err := f.WriteString(accessLine("u3", 50, 60)[:20]); err != nil { // partial, no newline
		t.Fatal(err)
	}
	got, err = tr.Poll()
	if err != nil || len(got) != 0 {
		t.Fatalf("partial line leaked: %+v err %v", got, err)
	}
	if _, err := f.WriteString(accessLine("u3", 50, 60)[20:]); err != nil { // newline arrives
		t.Fatal(err)
	}
	got, err = tr.Poll()
	if err != nil || len(got) != 1 || got[0].UserID != "u3" || got[0].Up != 50 {
		t.Fatalf("completed line lost: %+v err %v", got, err)
	}
	f.Close()
}

// Rotation (the file at the path shrank) restarts the tail at zero and reads
// the new file from its beginning.
func TestTailerRotation(t *testing.T) {
	t.Parallel()
	p := filepath.Join(t.TempDir(), "access.log")
	writeLog(t, p, accessLine("u1", 1, 1)+accessLine("u1", 2, 2))
	tr := NewTailer(p)
	if got, _ := tr.Poll(); len(got) != 0 {
		t.Fatalf("fresh tailer read %d entries", len(got))
	}

	// caddy renames the old file away and starts a new, shorter one.
	writeLog(t, p, accessLine("u2", 7, 8))
	got, err := tr.Poll()
	if err != nil || len(got) != 1 || got[0].UserID != "u2" {
		t.Fatalf("after rotation: %+v err %v", got, err)
	}
}

// A missing file is the quiet state of a lane that has not served anything
// yet — not an error, and the tailer picks the file up once it appears.
func TestTailerMissingFile(t *testing.T) {
	t.Parallel()
	p := filepath.Join(t.TempDir(), "access.log")
	tr := NewTailer(p)
	if got, err := tr.Poll(); err != nil || len(got) != 0 {
		t.Fatalf("missing file: %+v err %v", got, err)
	}
	writeLog(t, p, accessLine("u9", 3, 4))
	got, err := tr.Poll()
	if err != nil || len(got) != 1 || got[0].UserID != "u9" {
		t.Fatalf("after appearance: %+v err %v", got, err)
	}
}

// A persisted offset resumes the tail mid-file: traffic logged while the
// sampler was down is the users' and must still be accounted.
func TestNewTailerAtResumes(t *testing.T) {
	t.Parallel()
	p := filepath.Join(t.TempDir(), "access.log")
	first := accessLine("u1", 10, 20)
	writeLog(t, p, first+accessLine("u2", 30, 40))

	tr := NewTailerAt(p, int64(len(first)))
	got, err := tr.Poll()
	if err != nil || len(got) != 1 || got[0].UserID != "u2" || got[0].Up != 30 {
		t.Fatalf("resumed: %+v err %v", got, err)
	}
	if tr.Offset() != int64(len(first)+len(accessLine("u2", 30, 40))) {
		t.Errorf("Offset() = %d", tr.Offset())
	}

	// An offset past the file (rotated while nobody watched) restarts at zero.
	stale := NewTailerAt(p, 1<<20)
	got, err = stale.Poll()
	if err != nil || len(got) != 2 {
		t.Fatalf("stale offset: %+v err %v", got, err)
	}
}
