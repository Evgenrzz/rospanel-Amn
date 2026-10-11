package link

import (
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// The naive link carries the user's credential as u<id>:<pass> on the node's host and
// port, with the lane's label as the fragment. There is no ?query — NaiveProxy's URI has
// none — and the scheme is naive+https://, the form the clients import.
func TestNaiveLink(t *testing.T) {
	t.Parallel()
	set := &model.Settings{Host: "nl.example.com", ServerID: 5, NaivePort: 2053, NaiveName: "Naive NL"}
	u := model.User{ID: 7, NaivePass: "secret123"}

	const want = "naive+https://u7:secret123@nl.example.com:2053#Naive%20NL"
	if got := Naive(u, set); got != want {
		t.Fatalf("link:\n got %q\nwant %q", got, want)
	}

	// No password minted yet: nothing to hand out rather than a link that cannot
	// authenticate.
	if got := Naive(model.User{ID: 7}, set); got != "" {
		t.Errorf("a user with no password got a link: %q", got)
	}

	// A password with a reserved byte is escaped, so it survives the URI.
	escaped := Naive(model.User{ID: 7, NaivePass: "p@ss"}, set)
	if escaped != "naive+https://u7:p%40ss@nl.example.com:2053#Naive%20NL" {
		t.Errorf("an unescaped password broke the authority: %q", escaped)
	}
}
