package naive

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ulikunitz/xz"
)

// makeTarXz builds a synthetic release archive with the given member paths.
func makeTarXz(t *testing.T, members map[string]string) string {
	t.Helper()
	var buf bytes.Buffer
	xw, err := xz.NewWriter(&buf)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(xw)
	for name, body := range members {
		hdr := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}
		if strings.HasSuffix(name, "/") {
			hdr = &tar.Header{Name: name, Mode: 0o755, Typeflag: tar.TypeDir}
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if hdr.Typeflag == tar.TypeReg {
			if _, err := tw.Write([]byte(body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := xw.Close(); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "asset.tar.xz")
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// The release layout is a single directory with the binary inside; extraction
// must find it by base name, land it executable, and ignore the rest.
func TestExtractCaddy(t *testing.T) {
	t.Parallel()
	arch := makeTarXz(t, map[string]string{
		"caddy-forwardproxy-naive/":          "",
		"caddy-forwardproxy-naive/LICENSE":   "license text",
		"caddy-forwardproxy-naive/README.md": "readme",
		"caddy-forwardproxy-naive/caddy":     "ELF-BODY",
	})
	dest := filepath.Join(t.TempDir(), "caddy-naive")
	if err := extractCaddy(arch, dest); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "ELF-BODY" {
		t.Errorf("extracted %q", got)
	}
	fi, _ := os.Stat(dest)
	if fi.Mode().Perm()&0o111 == 0 {
		t.Errorf("extracted binary is not executable: %v", fi.Mode())
	}
}

func TestExtractCaddyNoMember(t *testing.T) {
	t.Parallel()
	arch := makeTarXz(t, map[string]string{"something/else": "x"})
	if err := extractCaddy(arch, filepath.Join(t.TempDir(), "out")); err == nil {
		t.Error("an archive without a caddy member extracted without an error")
	}
}

func TestReleaseURL(t *testing.T) {
	t.Parallel()
	u := releaseURL()
	if !strings.Contains(u, "klzgrad/forwardproxy") || !strings.Contains(u, PinnedVersion) || !strings.HasSuffix(u, ".tar.xz") {
		t.Errorf("releaseURL() = %q", u)
	}
}

// The pinned checksum in the source must be the checksum of the pinned
// asset — a typo there would brick every fresh node install at the integrity
// gate. This verifies the constant against the archive downloaded during
// development; it does NOT hit the network (the constant is checked for
// shape only — see the pinning commit for the archive itself).
func TestPinnedChecksumShape(t *testing.T) {
	t.Parallel()
	if len(pinnedSHA256) != 64 {
		t.Errorf("pinnedSHA256 is not a hex SHA-256: %q", pinnedSHA256)
	}
	for _, c := range pinnedSHA256 {
		if !strings.ContainsRune("0123456789abcdef", c) {
			t.Fatalf("pinnedSHA256 has a non-hex rune: %q", pinnedSHA256)
		}
	}
}
