package store

import (
	"path/filepath"
	"slices"
	"testing"
)

// rawNaivePass reads the naive_pass column exactly as it sits on disk, before decField:
// the only way to see whether a password is stored as ciphertext or leaked in clear.
func rawNaivePass(t *testing.T, st *Store, id int64) string {
	t.Helper()
	var raw string
	if err := st.db.QueryRow(`SELECT naive_pass FROM users WHERE id = ?`, id).Scan(&raw); err != nil {
		t.Fatalf("raw naive_pass for %d: %v", id, err)
	}
	return raw
}

// A NaiveProxy password is minted once and kept: a claim writes only where the column is
// still empty, stores it encrypted (this package always runs with a key — see
// main_test.go) and reads it back in clear, a deleted user is skipped rather than
// resurrected, and a regeneration overwrites what a claim would have left alone.
func TestNaivePassClaimSetAndIDs(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	mk := func(name string) int64 {
		t.Helper()
		u, err := st.CreateUser(name, "uuid-"+name, "pw", "tok-"+name, 0, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		return u.ID
	}
	a, b, c := mk("a"), mk("b"), mk("c")

	// A fresh user has no password: the column default is the empty string, and an
	// empty column is exactly what NaivePassUserIDs and the claim's guard key off.
	if raw := rawNaivePass(t, st, a); raw != "" {
		t.Fatalf("a fresh user's naive_pass is %q, want empty", raw)
	}
	if ids, err := st.NaivePassUserIDs(); err != nil || len(ids) != 0 {
		t.Fatalf("NaivePassUserIDs on a fresh store = (%v, %v), want none", ids, err)
	}

	// Claim mints for a and b and hands back what the store kept, in clear.
	got, err := st.ClaimUsersNaive([]NaiveClaim{{UserID: a, Pass: "pass-a"}, {UserID: b, Pass: "pass-b"}})
	if err != nil {
		t.Fatal(err)
	}
	if got[a] != "pass-a" || got[b] != "pass-b" {
		t.Fatalf("claim returned %+v, want a=pass-a b=pass-b", got)
	}
	// Encrypted at rest — the raw column is neither empty nor the plaintext — yet
	// GetUser decrypts it back, which is what the Caddyfile and the link need.
	if raw := rawNaivePass(t, st, a); raw == "" || raw == "pass-a" {
		t.Errorf("naive_pass at rest is %q, want a non-empty ciphertext", raw)
	}
	if u, err := st.GetUser(a); err != nil || u.NaivePass != "pass-a" {
		t.Errorf("GetUser naive_pass = %q (err %v), want the plaintext pass-a", u.NaivePass, err)
	}

	// A second claim keeps what the user already has: the WHERE naive_pass = '' guard
	// means a password is minted once, never silently rotated by another node's build.
	if again, err := st.ClaimUsersNaive([]NaiveClaim{{UserID: a, Pass: "another-a"}}); err != nil || again[a] != "pass-a" {
		t.Errorf("a second claim = (%+v, %v), want a kept pass-a", again, err)
	}

	// A deleted user is skipped, not brought back: the row is gone, so there is nothing
	// to write and nothing to return.
	if err := st.DeleteUser(c); err != nil {
		t.Fatal(err)
	}
	if skipped, err := st.ClaimUsersNaive([]NaiveClaim{{UserID: c, Pass: "pass-c"}}); err != nil {
		t.Fatal(err)
	} else if _, ok := skipped[c]; ok {
		t.Errorf("a deleted user came back with a password: %+v", skipped)
	}

	// The claimed set, in id order — the exact list a fleet-wide regeneration re-mints.
	ids, err := st.NaivePassUserIDs()
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{a, b}; !slices.Equal(ids, want) {
		t.Errorf("NaivePassUserIDs = %v, want %v", ids, want)
	}

	// SetUsersNaivePass overwrites unconditionally — a regeneration, not a claim — and
	// still stores the new value encrypted.
	if err := st.SetUsersNaivePass(map[int64]string{a: "regen-a", b: "regen-b"}); err != nil {
		t.Fatal(err)
	}
	for id, wantPass := range map[int64]string{a: "regen-a", b: "regen-b"} {
		u, err := st.GetUser(id)
		if err != nil {
			t.Fatal(err)
		}
		if u.NaivePass != wantPass {
			t.Errorf("user %d naive_pass = %q after regen, want %q", id, u.NaivePass, wantPass)
		}
		if raw := rawNaivePass(t, st, id); raw == "" || raw == wantPass {
			t.Errorf("user %d naive_pass at rest is %q, want a non-empty ciphertext", id, raw)
		}
	}
}

// WorkingCredentials is the projection every node's state is built from, so it has to
// carry the naive password in clear — a node renders it straight into the Caddyfile and
// the access-log reader matches it against the same "u<id>" name.
func TestWorkingCredentialsCarriesNaivePass(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	u, err := st.CreateUser("worker", "uuid-worker", "pw", "tok-worker", 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ClaimUsersNaive([]NaiveClaim{{UserID: u.ID, Pass: "pass-worker"}}); err != nil {
		t.Fatal(err)
	}
	creds, err := st.WorkingCredentials(1)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, c := range creds {
		if c.ID != u.ID {
			continue
		}
		found = true
		if c.NaivePass != "pass-worker" {
			t.Errorf("WorkingCredentials naive_pass = %q, want the decrypted pass-worker", c.NaivePass)
		}
	}
	if !found {
		t.Fatalf("user %d is missing from WorkingCredentials: %+v", u.ID, creds)
	}
}

// 0119 adds the naive columns to a database that never had them: an existing user comes
// through with an empty password and every other credential intact, the settings row
// gains zeroed defaults, and reopening the upgraded database replays nothing (the
// migration is recorded, so it neither re-runs nor fails on a duplicate column).
func TestNaiveMigrationUpgradesAndIsIdempotent(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "pre-naive.db")
	db := dbBeforeMigration(t, path, "0119")
	// A user from before the lane existed, holding an AmneziaWG key the upgrade must carry.
	if _, err := db.Exec(`INSERT INTO users (id, name, uuid, password, sub_token, wg_private_key)
		VALUES (1, 'old', 'uuid-old', 'pw', 'tok-old', 'wg-old')`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	st, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	u, err := st.GetUser(1)
	if err != nil {
		t.Fatal(err)
	}
	// The new column defaults to empty: nobody has a naive password until the lane mints
	// one, and the upgrade left the AmneziaWG key exactly as it was.
	if u.NaivePass != "" {
		t.Errorf("naive_pass after the upgrade = %q, want empty", u.NaivePass)
	}
	if u.WGPrivateKey != "wg-old" {
		t.Errorf("wg_private_key after the upgrade = %q, want it carried over wg-old", u.WGPrivateKey)
	}
	if ids, err := st.NaivePassUserIDs(); err != nil || len(ids) != 0 {
		t.Errorf("NaivePassUserIDs after the upgrade = (%v, %v), want none", ids, err)
	}
	// The settings row gained the naive defaults, all zero/empty — a lane nobody has
	// turned on yet.
	set, err := st.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	if set.NaiveEnabled || set.NaivePort != 0 || set.NaiveProbeSecret != "" || set.NaiveMasqueradeURL != "" {
		t.Errorf("settings naive defaults: on=%v port=%d probe=%q masq=%q, want all zero",
			set.NaiveEnabled, set.NaivePort, set.NaiveProbeSecret, set.NaiveMasqueradeURL)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Reopening an already-upgraded database must not re-run 0119 (an ADD COLUMN would
	// fail on the duplicate) — the ledger records the filename, so migrate skips it.
	again, err := Open(path)
	if err != nil {
		t.Fatalf("reopen (0119 must be idempotent): %v", err)
	}
	defer again.Close()
	if u2, err := again.GetUser(1); err != nil || u2.NaivePass != "" || u2.WGPrivateKey != "wg-old" {
		t.Errorf("after reopen: user %+v (err %v), want the same carried-over credentials", u2, err)
	}
}
