package naive

import (
	"bytes"
	"encoding/json"
)

// The caddy site-level access log is the lane's only traffic counter: unlike
// Xray's API or the WireGuard device counters, a naive server has nothing to
// poll — every handled request is one JSON line, and the authenticated
// CONNECT lines carry the username caddy checked the basic-auth against
// (request.user_id) plus the byte counters (bytes_read = what the client
// sent, size = what it received).
//
// Lines without a user_id are the masquerade doing its job (probes, scanners,
// browsers that found the port) and are counted against nobody.

// Entry is one accounted access-log line.
type Entry struct {
	// UserID is the username of the authenticated request (u<id>).
	UserID string
	// Up is bytes_read: the client's upload, in the orientation the panel's
	// traffic deltas use (server-side counters, user's view).
	Up int64
	// Down is size: the client's download.
	Down int64
	// ClientIP is where the request came from — the sighting source for the
	// device limit and the online status, same role the AWG endpoint plays.
	ClientIP string
	// TS is the request time, unix seconds.
	TS int64
}

// logLine is the subset of caddy's JSON log entry the lane reads. Everything
// else (headers, duration, status, TLS details) stays on disk.
type logLine struct {
	TS float64 `json:"ts"` // epoch seconds with fractional part
	// BytesRead is the request body size; Size is the response size. For a
	// CONNECT tunnel these are the two directions of the proxied stream.
	BytesRead int64 `json:"bytes_read"`
	Size      int64 `json:"size"`
	Request   struct {
		UserID   string `json:"user_id"`
		ClientIP string `json:"client_ip"`
		RemoteIP string `json:"remote_ip"`
	} `json:"request"`
}

// ParseLine reads one access-log line. ok is false for anything that is not
// an accounted request: unparseable JSON (a truncated last line, a foreign
// logger writing to the same file), and — deliberately — lines without a
// user_id, which are masquerade traffic that belongs to no user.
func ParseLine(line []byte) (Entry, bool) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 || line[0] != '{' {
		return Entry{}, false
	}
	var l logLine
	if err := json.Unmarshal(line, &l); err != nil {
		return Entry{}, false
	}
	if l.Request.UserID == "" {
		return Entry{}, false
	}
	ip := l.Request.ClientIP
	if ip == "" {
		ip = l.Request.RemoteIP
	}
	return Entry{
		UserID:   l.Request.UserID,
		Up:       l.BytesRead,
		Down:     l.Size,
		ClientIP: ip,
		TS:       int64(l.TS),
	}, true
}
