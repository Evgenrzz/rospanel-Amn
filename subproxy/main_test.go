package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// withIdentity must add user_id and leave the panel's own bytes as they were sent —
// a timestamp re-encoded as 1.8e+09 would still parse, but it is not what the panel
// wrote and not what a second consumer should have to cope with.
func TestWithIdentityKeepsViewIntact(t *testing.T) {
	in := []byte(`{"data":{"name":"Евген","expire_at":1801822202,"used_bytes":4341413434,` +
		`"used_pct":0,"limit_bytes":0,"online":true,"links":[{"name":"x","url":"vless://a<b>&c"}]}}`)
	out := withIdentity(in, 5)

	var got struct {
		Data struct {
			UserID   int64  `json:"user_id"`
			Name     string `json:"name"`
			ExpireAt int64  `json:"expire_at"`
			Used     int64  `json:"used_bytes"`
			Links    []struct {
				URL string `json:"url"`
			} `json:"links"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, out)
	}
	if got.Data.UserID != 5 || got.Data.Name != "Евген" || got.Data.ExpireAt != 1801822202 || got.Data.Used != 4341413434 {
		t.Fatalf("view mangled: %s", out)
	}
	if got.Data.Links[0].URL != "vless://a<b>&c" {
		t.Fatalf("link url = %q", got.Data.Links[0].URL)
	}
	if strings.Contains(string(out), "e+09") || strings.Contains(string(out), "E+09") {
		t.Fatalf("number re-encoded in exponent form: %s", out)
	}
}

// A view we cannot make sense of goes through untouched rather than becoming an error.
func TestWithIdentityPassthrough(t *testing.T) {
	for _, in := range []string{`not json`, `{"data":null}`, `{"data":[1,2]}`, `{"meta":{"total":1}}`} {
		if got := withIdentity([]byte(in), 5); string(got) != in {
			t.Errorf("withIdentity(%s) = %s", in, got)
		}
	}
}
