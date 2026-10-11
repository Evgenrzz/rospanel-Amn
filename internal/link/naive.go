package link

import (
	"fmt"
	"net/url"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/naive"
)

// Naive builds a NaiveProxy share link for one user on one node:
//
//	naive+https://u<id>:<pass>@<host>:<port>#<label>
//
// The scheme is naive+https:// — the form NekoBox/Matsuri, Shadowrocket and v2rayN
// import — and it matches the client config's "https://user:pass@host:port" proxy URL
// (see sub.NaiveClientConf): the "+https" names the transport the proxy URL dials. The
// username is always u<id> (naive.UserName, the same tag the node's access log and the
// Xray email use) and the password the user's minted NaivePass.
//
// Unlike every other lane there is no ?query — NaiveProxy's URI carries only the
// authority and the fragment — so this does not go through assemble, which always emits
// one. "" when the user has no naive password yet (the lane was never claimed for them),
// so a half-provisioned node hands out nothing rather than a link that cannot
// authenticate.
//
// NaiveProxy is node-only, so set is a node's materialised settings; the caller gates on
// that (the master never runs the lane and its NaiveEnabled stays off).
func Naive(u model.User, set *model.Settings) string {
	if u.NaivePass == "" {
		return ""
	}
	// UserPassword escapes both halves; the generated password is alphanumeric and the
	// username is u<digits>, so this is the identity here — but a regenerated or
	// hand-set password with a reserved byte still round-trips correctly.
	userinfo := url.UserPassword(naive.UserName(u.ID), u.NaivePass).String()
	return fmt.Sprintf("naive+https://%s@%s:%d#%s",
		userinfo, set.Host, set.NaivePort, url.PathEscape(LabelFor(model.ProtoNaive, u, set)))
}
