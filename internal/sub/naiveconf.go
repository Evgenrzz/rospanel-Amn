package sub

import (
	"fmt"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/naive"
)

// NaiveConfURL is where a user downloads their NaiveProxy client config for one node:
// <sub>/naive/<id>.conf; the QR of the import link is <id>.png. NaiveProxy is node-only,
// so id is a node's, never 0 — the master does not run the lane.
func NaiveConfURL(set *model.Settings, token string, serverID int64) string {
	return fmt.Sprintf("%s/naive/%d.conf", URL(set, token), serverID)
}

// NaiveFileName is the client config's file name, from the lane's label as AWGFileName
// makes one from its own.
func NaiveFileName(set *model.Settings) string {
	return confFileName(set.ProtoLabel(model.ProtoNaive), "naive")
}

// NaiveClientConf is the NaiveProxy client's config for one user on one node — the
// {"proxy":"https://u<id>:<pass>@<host>:<port>"} file the naiveproxy client and its forks
// import. It is the same credential the naive+https:// link carries (see link.Naive),
// rendered through naive.ClientConfig so the page and the download never drift. "" when
// the user has no password yet, so a half-provisioned lane serves nothing rather than a
// config that cannot authenticate.
func NaiveClientConf(u model.User, set *model.Settings) string {
	if u.NaivePass == "" {
		return ""
	}
	return naive.ClientConfig(naive.UserName(u.ID), u.NaivePass, set.Host, set.NaivePort)
}
