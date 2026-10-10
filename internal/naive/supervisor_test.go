package naive

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeCaddy installs a stand-in server binary in dir: a shell script that
// answers the three verbs the supervisor uses — run (stay alive, record the
// pid), validate and reload (succeed; failValidate flips validate to refuse) —
// plus the version marker that keeps EnsureBinary from downloading the real
// release.
func fakeCaddy(t *testing.T, dir string, failValidate bool) {
	t.Helper()
	val := "exit 0"
	if failValidate {
		val = "echo 'boom: syntax error' >&2; exit 1"
	}
	script := "#!/bin/sh\ncase \"$1\" in\n" +
		"run) echo $$ > \"" + filepath.Join(dir, "pid") + "\"; exec sleep 300;;\n" +
		"validate) " + val + ";;\n" +
		"reload) exit 0;;\n" +
		"esac\nexit 0\n"
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, BinaryName)
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin+".version", []byte(PinnedVersion+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readPID(t *testing.T, dir string) int {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "pid"))
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	return pid
}

// waitPID polls for the pid file the fake server writes once it is up: Apply
// returns as soon as the child is spawned, and the script needs a few
// scheduler ticks before its `echo $$` lands.
func waitPID(t *testing.T, dir string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(filepath.Join(dir, "pid")); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the fake server never wrote its pid file")
	return 0
}

// waitNewPID polls until the pid file differs from old — the relaunch after a
// crash — or the deadline runs out.
func waitNewPID(t *testing.T, dir string, old int) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got := readPID(t, dir); got != old {
			return got
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the pid file still reads %d — no relaunch", old)
	return 0
}

func testConfig(t *testing.T, dir string) Config {
	t.Helper()
	return Config{
		Port:     2053,
		CertFile: filepath.Join(dir, "cert.pem"),
		KeyFile:  filepath.Join(dir, "key.pem"),
		Users:    []User{{Name: "u1", Pass: "0123456789abcdef01234567"}},
	}
}

// Start, no-op re-apply, reload on change (the pid survives all of it: a user
// change must not drop live tunnels), and a quiet teardown.
func TestSupervisorApplyReloadStop(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the lane is linux-only (see EnsureBinary)")
	}
	dir := t.TempDir()
	fakeCaddy(t, dir, false)
	s := NewSupervisor(dir)
	// Registered before anything that can t.Fatal: a child left running would
	// outlive the temp dir (and the monitor would keep relaunching it into a
	// deleted directory).
	t.Cleanup(s.Stop)
	cfg := testConfig(t, dir)

	if err := s.Apply(cfg); err != nil {
		t.Fatal(err)
	}
	if !s.Running() {
		t.Fatal("not running after Apply")
	}
	pid := waitPID(t, dir)

	// The agent re-applies state on every user delta: an unchanged lane must
	// not blink.
	if err := s.Apply(cfg); err != nil {
		t.Fatal(err)
	}
	if got := readPID(t, dir); got != pid {
		t.Errorf("unchanged config restarted the server: pid %d -> %d", pid, got)
	}

	// A changed config reloads through the admin socket, not through a restart.
	cfg.Port = 2054
	if err := s.Apply(cfg); err != nil {
		t.Fatal(err)
	}
	if got := readPID(t, dir); got != pid {
		t.Errorf("a config change restarted instead of reloading: pid %d -> %d", pid, got)
	}
	cf, err := os.ReadFile(filepath.Join(dir, "Caddyfile"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cf), ":2054") {
		t.Errorf("the written Caddyfile did not carry the new port:\n%s", cf)
	}
	fi, _ := os.Stat(filepath.Join(dir, "Caddyfile"))
	if fi.Mode().Perm()&0o077 != 0 {
		t.Errorf("the Caddyfile is world/group-readable: %v (it holds plaintext passwords)", fi.Mode())
	}

	s.Stop()
	if s.Running() {
		t.Error("still running after Stop")
	}
	// A stopped lane stays stopped: no monitor relaunch.
	time.Sleep(100 * time.Millisecond)
	if s.Running() {
		t.Error("relaunched after Stop")
	}
}

// A config the binary itself rejects never reaches a spawn: the lane reports
// the refusal instead of coming up broken (or crash-looping).
func TestSupervisorRefusesInvalidConfig(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the lane is linux-only (see EnsureBinary)")
	}
	dir := t.TempDir()
	fakeCaddy(t, dir, true)
	s := NewSupervisor(dir)

	err := s.Apply(testConfig(t, dir))
	if err == nil {
		t.Fatal("an invalid config applied without an error")
	}
	if !strings.Contains(err.Error(), "rejected") {
		t.Errorf("error = %v", err)
	}
	if s.Running() {
		t.Error("the server came up on a rejected config")
	}
}

// A crashed server is relaunched by the monitor — the lane heals without the
// panel or the agent loop doing anything.
func TestSupervisorRestartsAfterCrash(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the lane is linux-only (see EnsureBinary)")
	}
	old := restartDelay
	restartDelay = 50 * time.Millisecond
	t.Cleanup(func() { restartDelay = old })

	dir := t.TempDir()
	fakeCaddy(t, dir, false)
	s := NewSupervisor(dir)
	t.Cleanup(s.Stop)
	if err := s.Apply(testConfig(t, dir)); err != nil {
		t.Fatal(err)
	}
	pid := waitPID(t, dir)
	proc, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	if err := proc.Signal(os.Kill); err != nil {
		t.Fatal(err)
	}
	// The monitor relaunches after restartDelay; the new instance rewrites the
	// pid file, which is the observable proof of the relaunch.
	waitNewPID(t, dir, pid)
	if !s.Running() {
		t.Error("the supervisor does not see the relaunched server")
	}
}
