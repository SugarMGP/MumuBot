package workerexec

import (
	"context"
	"encoding/json"
	"errors"
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
	uid         uint32
	gid         uint32
	groups      []uint32
	root        *os.Root
}

// New 在启动时确定唯一执行账号和 home，后续请求不能覆盖
func New(defaultUser, defaultHome, shell string) (*Server, error) {
	if defaultUser == "" {
		defaultUser = "worker"
	}
	u, err := user.Lookup(defaultUser)
	if err != nil {
		return nil, fmt.Errorf("找不到 worker 用户 %s: %w", defaultUser, err)
	}
	uid, err := strconv.ParseUint(u.Uid, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("worker 用户 UID 无效: %w", err)
	}
	gid, err := strconv.ParseUint(u.Gid, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("worker 用户 GID 无效: %w", err)
	}
	groups, _ := u.GroupIds()
	groupIDs := make([]uint32, 0, len(groups))
	for _, raw := range groups {
		id, convErr := strconv.ParseUint(raw, 10, 32)
		if convErr == nil && id > 0 {
			groupIDs = append(groupIDs, uint32(id))
		}
	}
	home := strings.TrimSpace(defaultHome)
	if home == "" {
		home = strings.TrimSpace(u.HomeDir)
	}
	if home == "" {
		return nil, fmt.Errorf("worker home 不能为空")
	}
	if err := os.MkdirAll(home, 0o755); err != nil {
		return nil, fmt.Errorf("创建 worker home 失败: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".tmp"), 0o755); err != nil {
		return nil, fmt.Errorf("创建 worker 临时目录失败: %w", err)
	}
	if shell == "" {
		shell = "/bin/bash"
	}
	root, err := os.OpenRoot(home)
	if err != nil {
		return nil, fmt.Errorf("打开 worker home 失败: %w", err)
	}
	return &Server{
		DefaultUser: defaultUser, DefaultHome: home, Shell: shell,
		uid: uint32(uid), gid: uint32(gid), groups: groupIDs, root: root,
	}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, workerproto.HealthResponse{OK: true})
	})
	mux.HandleFunc("/v1/exec", s.handleExec)
	mux.HandleFunc("/v1/files/list", s.handleFileList)
	mux.HandleFunc("/v1/files/read", s.handleFileRead)
	mux.HandleFunc("/v1/files/write", s.handleFileWrite)
	mux.HandleFunc("/v1/files/save", s.handleFileSave)
	mux.HandleFunc("/v1/files/content", s.handleFileContent)
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
	username := s.DefaultUser
	home := s.DefaultHome
	tmpDir := filepath.Join(home, ".tmp")

	timeout := req.TimeoutSeconds
	if timeout <= 0 {
		timeout = workerproto.DefaultTimeoutSeconds
	}
	if timeout > workerproto.MaxTimeoutSeconds {
		timeout = workerproto.MaxTimeoutSeconds
	}

	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return nil, err
	}
	stdoutFile, err := os.CreateTemp(tmpDir, "exec-stdout-*")
	if err != nil {
		return nil, err
	}
	stdoutPath := stdoutFile.Name()
	keepStdout := false
	defer func() {
		if !keepStdout {
			_ = os.Remove(stdoutPath)
		}
	}()
	stderrFile, err := os.CreateTemp(tmpDir, "exec-stderr-*")
	if err != nil {
		_ = os.Remove(stdoutPath)
		return nil, err
	}
	stderrPath := stderrFile.Name()
	keepStderr := false
	defer func() {
		if !keepStderr {
			_ = os.Remove(stderrPath)
		}
	}()

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
	if os.Geteuid() != int(s.uid) {
		sysProcAttr.Credential = &syscall.Credential{
			Uid: s.uid, Gid: s.gid, Groups: s.groups,
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

	_ = stdoutFile.Close()
	_ = stderrFile.Close()
	stdoutLog := relativeHomePath(home, stdoutPath)
	stderrLog := relativeHomePath(home, stderrPath)
	stdout, outTruncated, stdoutBytes := readLimited(stdoutPath, stdoutLog, workerproto.OutputLimitBytes)
	stderr, errTruncated, stderrBytes := readLimited(stderrPath, stderrLog, workerproto.OutputLimitBytes)
	keepStdout = outTruncated
	keepStderr = errTruncated
	exitCode := -1
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}
	if !outTruncated {
		stdoutLog = ""
	}
	if !errTruncated {
		stderrLog = ""
	}
	return &workerproto.ExecResponse{
		Stdout: stdout, Stderr: stderr,
		StdoutBytes: stdoutBytes, StderrBytes: stderrBytes,
		StdoutLog: stdoutLog, StderrLog: stderrLog,
		ExitCode: exitCode, TimedOut: timedOut, Truncated: outTruncated || errTruncated,
	}, nil
}

// killProcessGroup 先按 worker 权限杀进程组，遇到 sudo 子进程权限不足时走既有非交互 sudo
func killProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	if pid <= 0 {
		return
	}
	err := syscall.Kill(-pid, syscall.SIGKILL)
	if err != nil && errors.Is(err, syscall.EPERM) {
		_ = exec.Command("sudo", "-n", "/bin/kill", "-s", "KILL", "--", "-"+strconv.Itoa(pid)).Run()
	}
	_ = cmd.Process.Kill()
}

func relativeHomePath(home, path string) string {
	rel, err := filepath.Rel(home, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return path
	}
	return rel
}

// readLimited 保留有界 head+tail，并返回实际字节数和可续读日志路径
func readLimited(path, displayPath string, limit int64) (string, bool, int64) {
	f, err := os.Open(path)
	if err != nil {
		return "", false, 0
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", false, 0
	}
	size := info.Size()
	if size <= limit {
		data, readErr := io.ReadAll(f)
		if readErr != nil {
			return "", false, size
		}
		return string(data), false, size
	}
	headSize := limit * 3 / 4
	tailSize := limit - headSize
	head := make([]byte, headSize)
	tail := make([]byte, tailSize)
	if _, err := f.ReadAt(head, 0); err != nil && !errors.Is(err, io.EOF) {
		return "", false, size
	}
	if _, err := f.ReadAt(tail, size-tailSize); err != nil && !errors.Is(err, io.EOF) {
		return "", false, size
	}
	marker := fmt.Sprintf("\n\n[输出共 %d 字节，超过单流上限 %d；已保留前 %d 字节和后 %d 字节；完整输出见 %s]\n\n", size, limit, headSize, tailSize, displayPath)
	return string(head) + marker + string(tail), true, size
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
