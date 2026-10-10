package naive

import (
	"bytes"
	"io"
	"os"
)

// maxPollBytes caps how much one Poll reads. Between two samples of a busy
// lane the log grows by kilobytes; the cap is there for the pathological case
// (an agent that was down for a week) so the first poll after it comes back
// cannot allocate the whole file at once. What stays behind is picked up by
// the next poll — the offset only advances by what was consumed.
const maxPollBytes = 8 << 20

// Tailer reads an access log forward, remembering how far it got. The lane
// samples it on a tick (the node agent's traffic-report rhythm), so the reader
// has to survive everything a long-lived log file does:
//
//   - growth between polls — only the new tail is read;
//   - a partial last line (the writer is mid-append) — the fragment is kept
//     in memory until its newline shows up;
//   - rotation: caddy renames the file at roll_size and starts a new one at
//     the same path, which looks like the file SHRANK. The tail restarts at
//     zero then. The unread tail of the rotated-away file is lost — at a
//     50mb roll size and a seconds-apart sampling rhythm that is a rounding
//     error next to the alternative (tracking inodes and chasing renamed
//     files), and it can only ever UNDERcount, never double-count.
//
// Not safe for concurrent use; one sampler owns one Tailer.
type Tailer struct {
	path    string
	offset  int64  // file bytes consumed into entries or partial
	partial []byte // prefix of a line whose newline has not arrived
}

// NewTailer returns a reader positioned at the END of the log: history
// written before the lane came up was accounted by whoever ran before (or
// belongs to a deployment the panel never saw), and replaying it would
// double-charge users on every agent restart.
func NewTailer(path string) *Tailer {
	t := &Tailer{path: path}
	if fi, err := os.Stat(path); err == nil {
		t.offset = fi.Size()
	}
	return t
}

// Poll consumes what the writer appended since the last call and returns the
// accounted entries in it. A missing file is not an error — the lane may
// simply not have served a request yet; the tailer restarts at zero and picks
// the file up when it appears.
func (t *Tailer) Poll() ([]Entry, error) {
	f, err := os.Open(t.path)
	if os.IsNotExist(err) {
		t.offset, t.partial = 0, nil
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if fi.Size() < t.offset { // rotated: a new file lives at the path now
		t.offset, t.partial = 0, nil
	}
	avail := fi.Size() - t.offset
	if avail <= 0 {
		return nil, nil
	}
	if avail > maxPollBytes {
		avail = maxPollBytes
	}
	if _, err := f.Seek(t.offset, io.SeekStart); err != nil {
		return nil, err
	}
	buf := make([]byte, avail)
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return nil, err
	}
	buf = buf[:n]
	t.offset += int64(n)

	data := append(t.partial, buf...)
	t.partial = nil
	last := bytes.LastIndexByte(data, '\n')
	if last < 0 {
		// No complete line yet; keep the fragment (bounded: a line without
		// a newline that outgrows the file cap is not an access log).
		if len(data) <= maxPollBytes {
			t.partial = data
		}
		return nil, nil
	}
	t.partial = append([]byte(nil), data[last+1:]...)

	var out []Entry
	for _, line := range bytes.Split(data[:last], []byte{'\n'}) {
		if e, ok := ParseLine(line); ok {
			out = append(out, e)
		}
	}
	return out, nil
}
