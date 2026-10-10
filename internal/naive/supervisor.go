package naive

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Supervisor runs the lane's caddy-naive child process, in the pattern the
// node agent already supervises its opera-proxy helper with (internal/opera):
// Start replaces a running instance, an unexpected exit is relaunched, and
// Stop terminates and disables restarts.
//
// On top of that it owns the config handoff: Apply renders the Caddyfile,
// writes it atomically next to the binary, and — because a config change here
// means a user was added, expired or regenerated — reloads the running server
// through its admin socket instead of restarting it. Live tunnels survive a
// user change; only a binary or process failure bounces them.
type Supervisor struct {
	dir string // the lane's own directory: binary, Caddyfile, access log, admin socket

	mu      sync.Mutex
	cmd     *exec.Cmd
	done    chan struct{} // closed by the monitor once cmd.Wait reaps the child
	running bool          // child currently alive (guarded; cmd.ProcessState is written by Wait off-lock)
	epoch   int           // bumped on every start/stop; stale monitors exit
	active  bool          // the lane should be running (gates auto-restart)
	bin     string        // resolved binary path (after the first successful Apply)
	applied string        // the Caddyfile content currently in force
}

// restartDelay is how long the monitor waits before relaunching a crashed
// server. Fixed, as in the opera supervisor: caddy exiting on its own is rare
// (the config it came up with was validated), and when it does — an OOM kill,
// a transient bind failure — a prompt retry is the whole job.
var restartDelay = 5 * time.Second

// NewSupervisor builds a supervisor for the lane rooted at dir. The directory
// is created (0700: the Caddyfile inside holds every user's plaintext
// password) on the first Apply.
func NewSupervisor(dir string) *Supervisor { return &Supervisor{dir: dir} }

// LogPath is the access log the supervisor's Caddyfile writes and the traffic
// sampler tails. Fixed per directory so both sides agree without exchanging
// the path.
func (s *Supervisor) LogPath() string { return filepath.Join(s.dir, "access.log") }

// caddyfilePath is the rendered config's home.
func (s *Supervisor) caddyfilePath() string { return filepath.Join(s.dir, "Caddyfile") }

// adminSocket is the unix socket caddy's admin API listens on, through which
// Apply reloads. Filesystem permissions on the 0700 dir are the access
// control.
func (s *Supervisor) adminSocket() string { return filepath.Join(s.dir, "admin.sock") }

// Apply brings the child process in line with cfg: the binary is ensured, the
// Caddyfile rendered and written, and the server reloaded (running) or
// started (not). An unchanged config is a no-op — the agent re-applies state
// on every user delta, and a lane that did not change must not blink.
//
// A config the server itself rejects (validate fails) is refused BEFORE
// anything in flight is touched: the running instance keeps serving the
// previous config, and the caller gets the error to report.
func (s *Supervisor) Apply(cfg Config) error {
	bin, err := EnsureBinary(s.dir)
	if err != nil {
		return err
	}
	cfg.LogFile = s.LogPath()
	cfg.AdminSocket = s.adminSocket()
	rendered, err := Render(cfg)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.bin = bin
	if s.running && rendered == s.applied {
		return nil // nothing changed; don't touch the live server
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	if err := writeAtomic(s.caddyfilePath(), []byte(rendered), 0o600); err != nil {
		return err
	}
	// Ask the binary itself whether the config means what we think before a
	// restart puts it in force. Skipped for a reload: caddy reload parses the
	// file too, and a refusal there leaves the old config running — but a
	// spawn on a broken config would leave the lane DOWN, so the spawn path
	// always validates first.
	if s.running {
		if err := s.reloadLocked(); err != nil {
			slog.Warn("naive: reload failed — restarting the server instead", "err", err)
		} else {
			s.applied = rendered
			return nil
		}
	} else if err := s.validateLocked(); err != nil {
		return fmt.Errorf("naive: the server rejected its config: %w", err)
	}
	s.epoch++
	s.killLocked()
	s.active = true
	if err := s.spawnLocked(); err != nil {
		return err
	}
	s.applied = rendered
	return nil
}

// Stop terminates the server and disables auto-restart. Missing file, dead
// process and a lane that was never up all land in the same quiet state.
func (s *Supervisor) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.epoch++
	s.active = false
	s.killLocked()
	s.applied = ""
}

// Running reports whether the server process is currently alive.
func (s *Supervisor) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

// reloadLocked hands the written Caddyfile to the running server through its
// admin socket. Zero downtime: caddy swaps listeners in place. Caller holds
// s.mu; the reload is a short-lived child of its own.
func (s *Supervisor) reloadLocked() error {
	out, err := exec.Command(s.bin, "reload",
		"--config", s.caddyfilePath(),
		"--adapter", "caddyfile",
		"--address", "unix/"+s.adminSocket(),
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, firstLine(out))
	}
	return nil
}

// validateLocked asks the binary whether the written Caddyfile parses, so a
// bad config never reaches a spawn. Caller holds s.mu.
func (s *Supervisor) validateLocked() error {
	out, err := exec.Command(s.bin, "validate",
		"--config", s.caddyfilePath(),
		"--adapter", "caddyfile",
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, firstLine(out))
	}
	return nil
}

// spawnLocked starts the server and its monitor. Caller holds s.mu.
func (s *Supervisor) spawnLocked() error {
	epoch := s.epoch
	cmd := exec.Command(s.bin, "run",
		"--config", s.caddyfilePath(),
		"--adapter", "caddyfile",
	)
	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("naive: start caddy-naive: %w", err)
	}
	done := make(chan struct{})
	s.cmd = cmd
	s.done = done
	s.running = true
	go logNaiveLines(stderr)
	go s.monitor(cmd, epoch, done)
	slog.Info("naive: server started", "pid", cmd.Process.Pid)
	return nil
}

// monitor waits for the child to exit and relaunches it after a delay,
// unless it was superseded (newer epoch) or intentionally stopped.
func (s *Supervisor) monitor(cmd *exec.Cmd, epoch int, done chan struct{}) {
	err := cmd.Wait()
	close(done) // tell killLocked the port is freed before it can return
	s.mu.Lock()
	if epoch != s.epoch {
		s.mu.Unlock()
		return // a newer Apply/Stop took over; it owns s.running
	}
	s.running = false
	active := s.active
	s.mu.Unlock()
	if !active {
		return
	}
	slog.Warn("naive: server exited — restarting", "err", err, "in", restartDelay)
	time.Sleep(restartDelay)
	s.mu.Lock()
	defer s.mu.Unlock()
	if epoch != s.epoch || !s.active {
		return
	}
	if err := s.spawnLocked(); err != nil {
		slog.Error("naive: restart failed", "err", err)
	}
}

// killLocked terminates the current child (if any) and waits for it to be
// reaped, so the listen port is actually free for the next spawn. SIGTERM
// first — caddy drains live tunnels on a graceful stop — SIGKILL only if the
// grace runs out. Caller holds s.mu.
func (s *Supervisor) killLocked() {
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Signal(os.Interrupt)
		select {
		case <-s.done:
		case <-time.After(3 * time.Second):
			_ = s.cmd.Process.Kill()
			if s.done != nil {
				select {
				case <-s.done:
				case <-time.After(2 * time.Second):
				}
			}
		}
	}
	s.cmd = nil
	s.done = nil
	s.running = false
}

// writeAtomic writes a file via temp + rename, so a reader (caddy reloading,
// an operator catting the config) never sees a half-written file.
func writeAtomic(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".new"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	if err := os.Chmod(tmp, perm); err != nil { // WriteFile's perm is pre-umask
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// logNaiveLines forwards the server's stderr to the agent's log. The
// Caddyfile pins its level to ERROR, so this stream stays quiet until
// something is genuinely wrong — and then it is what the operator's log
// viewer shows.
func logNaiveLines(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 16*1024), 64*1024)
	for sc.Scan() {
		slog.Error("naive: " + sc.Text())
	}
}

// firstLine is a command's output cut to its first line, for error strings
// that travel to an admin's chat and must stay bounded.
func firstLine(out []byte) string {
	s := string(bytes.TrimSpace(out))
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
