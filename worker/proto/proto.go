package workerproto

const (
	DefaultTimeoutSeconds = 300
	MaxTimeoutSeconds     = 1800
	OutputLimitBytes      = 64 * 1024
)

type ExecRequest struct {
	SessionID      uint   `json:"session_id"`
	Command        string `json:"command"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

type ExecResponse struct {
	Stdout      string `json:"stdout"`
	Stderr      string `json:"stderr"`
	StdoutBytes int64  `json:"stdout_bytes"`
	StderrBytes int64  `json:"stderr_bytes"`
	StdoutLog   string `json:"stdout_log,omitempty"`
	StderrLog   string `json:"stderr_log,omitempty"`
	ExitCode    int    `json:"exit_code"`
	TimedOut    bool   `json:"timed_out"`
	Truncated   bool   `json:"truncated"`
}

type HealthResponse struct {
	OK bool `json:"ok"`
}

type ErrorResponse struct {
	Error string `json:"error"`
}
