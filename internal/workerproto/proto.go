package workerproto

const (
	DefaultTimeoutSeconds = 300
	MaxTimeoutSeconds     = 1800
	OutputLimitBytes      = 64 * 1024
)

type ExecRequest struct {
	SessionID      uint   `json:"session_id"`
	User           string `json:"user,omitempty"`
	Home           string `json:"home,omitempty"`
	Command        string `json:"command"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

type ExecResponse struct {
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
	ExitCode  int    `json:"exit_code"`
	TimedOut  bool   `json:"timed_out"`
	Truncated bool   `json:"truncated"`
}

type HealthResponse struct {
	OK bool `json:"ok"`
}

type ErrorResponse struct {
	Error string `json:"error"`
}
