package naive

import (
	"archive/tar"
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/ulikunitz/xz"
)

// The server binary is Caddy built with the klzgrad forwardproxy-naive plugin
// ("caddy-naive" on the node, after the reference deployment). It is pinned
// and downloaded exactly like internal/xray pins Xray: a fresh box comes up
// without a separate install step, a panel update can carry a new pin, and an
// artifact that does not match the pinned checksum is never executed.
//
// The pin lives in klzgrad/forwardproxy, NOT klzgrad/naiveproxy: the
// naiveproxy releases ship the client only — the server builds are published
// by the plugin fork's own releases.
const (
	// PinnedVersion is the klzgrad/forwardproxy release tag.
	PinnedVersion = "v2.11.7-naive"
	// AssetName is the single server asset of that release (linux/amd64,
	// statically linked — the release publishes no other server build).
	AssetName = "caddy-forwardproxy-naive.tar.xz"
	// pinnedSHA256 is the checksum of AssetName at PinnedVersion.
	pinnedSHA256 = "ac97057c925bb098eb575e7b0cb8bd547d4c5f845b09a3dedfbe34444aa7ae88"
)

// ErrUnsupported is what a platform with no pinned server build gets (anything
// but linux/amd64, and a developer's laptop). The lane reports it once and
// stays dark instead of crash-looping — the same contract internal/awg has
// for a build without TUN support.
var ErrUnsupported = errors.New("naive: no pinned caddy-naive build for this platform")

// BinaryName is what the server binary is installed as.
const BinaryName = "caddy-naive"

// releaseURL is the download URL of the pinned asset.
func releaseURL() string {
	return "https://github.com/klzgrad/forwardproxy/releases/download/" + PinnedVersion + "/" + AssetName
}

// EnsureBinary returns the path to the caddy-naive binary in dir, downloading
// the pinned release there if one is not already present — or if the one
// present was installed from a different pin (a marker file next to the
// binary records the pin; `caddy version` reports the upstream Caddy version,
// which does not move with the plugin build, so it cannot tell two pins of
// the same Caddy apart).
//
// An operator who placed their own build at dir/caddy-naive AND wrote the
// matching marker keeps it; without the marker the pinned release replaces
// whatever is there, which is the same trade internal/xray makes.
func EnsureBinary(dir string) (string, error) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return "", ErrUnsupported
	}
	dest := filepath.Join(dir, BinaryName)
	marker := dest + ".version"
	have := ""
	if fi, err := os.Stat(dest); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
		if b, err := os.ReadFile(marker); err == nil && strings.TrimSpace(string(b)) == PinnedVersion {
			return dest, nil // already the pinned release
		}
		slog.Info("naive: installed caddy-naive differs from the pinned release — upgrading",
			"want", PinnedVersion, "path", dest)
		have = dest
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return have, orKeep(have, err)
	}
	if err := download(dest, dir); err != nil {
		return have, orKeep(have, err)
	}
	if err := os.WriteFile(marker, []byte(PinnedVersion+"\n"), 0o644); err != nil {
		// The binary is in place; a missing marker only means the next start
		// re-downloads. Loud, but not fatal.
		slog.Warn("naive: version marker not written", "err", err)
	}
	return dest, nil
}

// download fetches, checksum-gates and unpacks the pinned release into dest.
func download(dest, dir string) error {
	url := releaseURL()
	client := &http.Client{Timeout: 300 * time.Second} // the asset is ~35 MB
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}
	tmpArch, err := os.CreateTemp(dir, "caddy-naive-*.tar.xz")
	if err != nil {
		return err
	}
	defer os.Remove(tmpArch.Name())
	n, err := io.Copy(tmpArch, resp.Body)
	tmpArch.Close()
	if err != nil {
		return err
	}
	if resp.ContentLength >= 0 && n != resp.ContentLength {
		return fmt.Errorf("download truncated: got %d of %d bytes", n, resp.ContentLength)
	}
	// Integrity gate: the archive must match the pinned SHA-256 before its
	// contents are unpacked and run as root. Refuse otherwise.
	sum, err := sha256File(tmpArch.Name())
	if err != nil {
		return err
	}
	if !strings.EqualFold(sum, pinnedSHA256) {
		return fmt.Errorf("caddy-naive %s checksum mismatch (got %s, want %s) — refusing to install",
			PinnedVersion, sum, pinnedSHA256)
	}
	return extractCaddy(tmpArch.Name(), dest)
}

// extractCaddy unpacks the single "caddy" member of the tar.xz into dest
// (written via a temp file and renamed, so a half-copied binary can never be
// what the supervisor starts).
func extractCaddy(archive, dest string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	xr, err := xz.NewReader(bufio.NewReader(f))
	if err != nil {
		return err
	}
	tr := tar.NewReader(xr)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if h.Typeflag != tar.TypeReg || filepath.Base(h.Name) != "caddy" {
			continue
		}
		tmp := dest + ".new"
		out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
		if err != nil {
			return err
		}
		_, err = io.Copy(out, tr)
		out.Close()
		if err != nil {
			os.Remove(tmp)
			return err
		}
		if err := os.Chmod(tmp, 0o755); err != nil {
			os.Remove(tmp)
			return err
		}
		if err := os.Rename(tmp, dest); err != nil {
			os.Remove(tmp)
			return err
		}
		return nil
	}
	return fmt.Errorf("no caddy binary inside %s", archive)
}

// orKeep swallows a download failure when a usable binary is already
// installed: an upgrade that cannot reach GitHub is a reason to stay on the
// release that works, not to take the lane down. With nothing installed the
// error stands, and the lane reports it in its status.
func orKeep(have string, err error) error {
	if have == "" {
		return err
	}
	slog.Warn("naive: staying on the installed caddy-naive — could not fetch the pinned one",
		"want", PinnedVersion, "path", have, "err", err)
	return nil
}

// sha256File returns the hex SHA-256 of the file at path.
func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
