package trace

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const resolvPath = "/etc/resolv.conf"

// startDNS launches the DNS logger (titra dns) in the background and points
// the system resolver at it, so connections can be shown by domain name.
func startDNS(st *State, opt Options) error {
	cur, err := os.ReadFile(resolvPath)
	if err != nil {
		return err
	}
	upstream := ""
	var keep []string
	for _, l := range strings.Split(string(cur), "\n") {
		f := strings.Fields(l)
		if len(f) >= 2 && f[0] == "nameserver" {
			if upstream == "" && f[1] != "127.0.0.1" {
				upstream = f[1]
			}
			continue
		}
		if len(f) >= 1 && (f[0] == "search" || f[0] == "options" || f[0] == "domain") {
			keep = append(keep, l)
		}
	}
	if upstream == "" {
		return fmt.Errorf("no upstream nameserver in %s", resolvPath)
	}

	logPath := filepath.Join(opt.StateDir, "dns.jsonl")
	ready := filepath.Join(opt.StateDir, "dns.ready")
	_ = os.Remove(ready)
	out, _ := os.OpenFile(filepath.Join(opt.StateDir, "dns.out"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	cmd := exec.Command(opt.Titra, "dns",
		"--listen", "127.0.0.1:53",
		"--upstream", upstream,
		"--log", logPath,
		"--ready", ready)
	cmd.Stdout, cmd.Stderr = out, out
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	_ = os.WriteFile(filepath.Join(opt.StateDir, "dns.pid"), []byte(fmt.Sprint(cmd.Process.Pid)), 0o644)
	go func() { _ = cmd.Wait() }()

	ok := false
	for i := 0; i < 50; i++ {
		if _, err := os.Stat(ready); err == nil {
			ok = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ok {
		_ = cmd.Process.Kill()
		b, _ := os.ReadFile(filepath.Join(opt.StateDir, "dns.out"))
		return fmt.Errorf("dns logger did not start: %s", strings.TrimSpace(string(b)))
	}

	bk := &ResolvBackup{Path: resolvPath, Content: string(cur)}
	if t, err := os.Readlink(resolvPath); err == nil {
		bk.LinkTarget = t
	}
	content := "# set by titra: DNS goes through the titra logger, then to " + upstream + "\nnameserver 127.0.0.1\n"
	for _, l := range keep {
		content += l + "\n"
	}
	tmp := resolvPath + ".titra"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		_ = cmd.Process.Kill()
		return err
	}
	if err := os.Rename(tmp, resolvPath); err != nil { // replaces a symlink atomically
		_ = cmd.Process.Kill()
		return err
	}
	st.DNS, st.DNSLog, st.Resolv = true, logPath, bk
	return nil
}

func restoreResolv(bk *ResolvBackup) {
	if bk.LinkTarget != "" {
		_ = os.Remove(bk.Path)
		_ = os.Symlink(bk.LinkTarget, bk.Path)
		return
	}
	_ = os.WriteFile(bk.Path, []byte(bk.Content), 0o644)
}
