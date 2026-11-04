package workerexec

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"mumu-bot/internal/workerproto"
)

type Server struct {
	DefaultUser string
	DefaultHome string
	Shell       string
}

func New(defaultUser, defaultHome, shell string) *Server {
	if defaultUser == "" {
		defaultUser = "worker"
	}
	if defaultHome == "" {
		defaultHome = "/home/worker"
	}
	if shell == "" {
		shell = "/bin/bash"
	}
	return &Server{DefaultUser: defaultUser, DefaultHome: defaultHome, Shell: shell}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, workerproto.HealthResponse{OK: true})
	})
	mux.HandleFunc("/v1/exec", s.handleExec)
	return mux
}

func (s *Server) handleExec(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req workerproto.ExecRequest
	body := http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, workerproto.ErrorResponse{Error: "invalid request"})
		return
	}
	if req.SessionID == 0 || strings.TrimSpace(req.Command) == "" {
		writeJSON(w, http.StatusBadRequest, workerproto.ErrorResponse{Error: "session_id and command are required"})
		return
	}
	resp, err := s.Exec(r.Context(), req)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, workerproto.ErrorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) Exec(ctx context.Context, req workerproto.ExecRequest) (*workerproto.ExecResponse, error) {
	username := strings.TrimSpace(req.User)
	if username == "" {
		username = s.DefaultUser
	}
	u, err := user.Lookup(username)
	if err != nil {
		return nil, fmt.Errorf("找不到 worker 用户 %s: %w", username, err)
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return nil, fmt.Errorf("worker 用户 UID 无效: %w", err)
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return nil, fmt.Errorf("worker 用户 GID 无效: %w", err)
	}
	groups, _ := u.GroupIds()
	groupIDs := make([]uint32, 0, len(groups))
	for _, raw := range groups {
		id, convErr := strconv.Atoi(raw)
		if convErr == nil && id > 0 {
			groupIDs = append(groupIDs, uint32(id))
		}
	}

	home := strings.TrimSpace(req.Home)
	if home == "" {
		home = strings.TrimSpace(u.HomeDir)
	}
	if home == "" {
		home = s.DefaultHome
	}
	if err := os.MkdirAll(home, 0o755); err != nil {
		return nil, fmt.Errorf("创建 worker home 失败: %w", err)
	}
	tmpDir := filepath.Join(home, ".tmp")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return nil, fmt.Errorf("创建 worker 临时目录失败: %w", err)
	}

	timeout := req.TimeoutSeconds
	if timeout <= 0 {
		timeout = workerproto.DefaultTimeoutSeconds
	}
	if timeout > workerproto.MaxTimeoutSeconds {
		timeout = workerproto.MaxTimeoutSeconds
	}

	stdoutFile, err := os.CreateTemp("", "mumu-worker-stdout-*")
	if err != nil {
		return nil, err
	}
	stdoutPath := stdoutFile.Name()
	defer os.Remove(stdoutPath)
	defer stdoutFile.Close()
	stderrFile, err := os.CreateTemp("", "mumu-worker-stderr-*")
	if err != nil {
		return nil, err
	}
	stderrPath := stderrFile.Name()
	defer os.Remove(stderrPath)
	defer stderrFile.Close()

	cmd := exec.Command(s.Shell, "-lc", req.Command)
	cmd.Dir = home
	cmd.Stdin = nil
	cmd.Stdout = stdoutFile
	cmd.Stderr = stderrFile
	cmd.Env = []string{
		"HOME=" + home,
		"USER=" + username,
		"LOGNAME=" + username,
		"SHELL=" + s.Shell,
		"PATH=" + filepath.Join(home, ".nix-profile", "bin") + ":/nix/var/nix/profiles/default/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"TMPDIR=" + tmpDir,
		"LANG=C.UTF-8",
		"TZ=Asia/Shanghai",
		"NIX_REMOTE=daemon",
		"PLAYWRIGHT_BROWSERS_PATH=" + filepath.Join(home, ".cache", "ms-playwright"),
	}
	sysProcAttr := &syscall.SysProcAttr{}
	if runtime.GOOS == "linux" {
		sysProcAttr.Setpgid = true
	}
	if os.Geteuid() != uid {
		sysProcAttr.Credential = &syscall.Credential{
			Uid: uint32(uid), Gid: uint32(gid), Groups: groupIDs,
		}
	}
	cmd.SysProcAttr = sysProcAttr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("启动命令失败: %w", err)
	}

	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()
	timer := time.NewTimer(time.Duration(timeout) * time.Second)
	defer timer.Stop()
	timedOut := false
	select {
	case <-timer.C:
		timedOut = true
		killProcessGroup(cmd)
	case <-ctx.Done():
		timedOut = true
		killProcessGroup(cmd)
	case <-waitCh:
	}
	if timedOut {
		select {
		case <-waitCh:
		case <-time.After(2 * time.Second):
		}
	}

	stdout, outTruncated := readLimited(stdoutPath, workerproto.OutputLimitBytes)
	stderr, errTruncated := readLimited(stderrPath, workerproto.OutputLimitBytes)
	exitCode := -1
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}
	return &workerproto.ExecResponse{
		Stdout: stdout, Stderr: stderr, ExitCode: exitCode,
		TimedOut: timedOut, Truncated: outTruncated || errTruncated,
	}, nil
}

func killProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	if pid <= 0 {
		return
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	_ = cmd.Process.Kill()
}

func readLimited(path string, limit int64) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return "", false
	}
	if int64(len(data)) > limit {
		return string(data[:limit]), true
	}
	return string(data), false
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
