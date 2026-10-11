package naive

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
)

// ClientConfig renders one user's NaiveProxy client config — the file the naiveproxy
// client (klzgrad) and its forks import:
//
//	{"proxy":"https://<user>:<pass>@<host>:<port>"}
//
// It is the same credential the naive+https:// share link carries (see link.Naive), in
// config-file form; the proxy URL is https://, the transport the link's "+https" names.
// The user and password are url-escaped, so a reserved byte in either survives. Both the
// subscription page (sub.NaiveClientConf) and the panel's config download
// (core.NaiveClientConfig) render through here, so the two can never drift apart.
func ClientConfig(user, pass, host string, port int) string {
	proxy := fmt.Sprintf("https://%s@%s",
		url.UserPassword(user, pass).String(),
		net.JoinHostPort(host, strconv.Itoa(port)))
	raw, err := json.Marshal(map[string]string{"proxy": proxy})
	if err != nil {
		return "" // a map[string]string never fails to marshal; keep the contract honest
	}
	return string(raw)
}
