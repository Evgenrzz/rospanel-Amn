package naive

import "testing"

func TestParseLine(t *testing.T) {
	t.Parallel()
	line := []byte(`{"level":"info","ts":1760000000.123,"logger":"http.log.access.log0",` +
		`"msg":"handled request","request":{"remote_ip":"1.2.3.4","remote_port":"52000",` +
		`"client_ip":"1.2.3.4","proto":"HTTP/2.0","method":"CONNECT","host":"vpn.example.com:2053",` +
		`"uri":"example.org:443"},"bytes_read":1000,"user_id":"u7","status":200,"duration":30.5,"size":20000}`)
	e, ok := ParseLine(line)
	if !ok {
		t.Fatal("a full access-log line did not parse")
	}
	if e.UserID != "u7" || e.Up != 1000 || e.Down != 20000 || e.ClientIP != "1.2.3.4" || e.TS != 1760000000 {
		t.Errorf("entry = %+v", e)
	}
}

// Traffic for the masquerade has no user_id and must be counted against
// nobody — otherwise a scanner hammering the fake site would charge whoever
// the parser falls back to.
func TestParseLineNoUser(t *testing.T) {
	t.Parallel()
	for _, line := range []string{
		`{"ts":1760000000,"request":{"client_ip":"9.9.9.9","method":"GET","uri":"/"},"bytes_read":0,"status":200,"size":512}`,
		`{"level":"error","ts":1760000000,"msg":"some runtime error"}`,
		`not json at all`,
		``,
		`{"ts":1,"request":{"user_id":"u3"},"bytes_read":5,"size":6}`, // parseable — belongs to the positive case below
	} {
		e, ok := ParseLine([]byte(line))
		want := line == `{"ts":1,"request":{"user_id":"u3"},"bytes_read":5,"size":6}`
		if ok != want {
			t.Errorf("ParseLine(%q) ok = %v, want %v", line, ok, want)
		}
		if want && (e.UserID != "u3" || e.Up != 5 || e.Down != 6) {
			t.Errorf("minimal line: entry = %+v", e)
		}
	}
}

// caddy fills client_ip from the trusted-proxy chain and remote_ip always;
// the parser prefers client_ip but must fall back rather than drop the
// sighting.
func TestParseLineIPFallback(t *testing.T) {
	t.Parallel()
	e, ok := ParseLine([]byte(`{"ts":1,"request":{"remote_ip":"8.8.8.8","user_id":"u1"},"bytes_read":1,"size":2}`))
	if !ok || e.ClientIP != "8.8.8.8" {
		t.Errorf("fallback: ok=%v entry=%+v", ok, e)
	}
}
