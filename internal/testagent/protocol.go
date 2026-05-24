package testagent

import "time"

const (
	EnvMode       = "SPORE_FAKE_AGENT_MODE"
	EnvEventLog   = "SPORE_FAKE_AGENT_EVENT_LOG"
	EnvReadyFile  = "SPORE_FAKE_AGENT_READY_FILE"
	EnvExitFile   = "SPORE_FAKE_AGENT_EXIT_FILE"
	EnvTranscript = "SPORE_FAKE_AGENT_TRANSCRIPT"
	EnvTurnLimit  = "SPORE_FAKE_AGENT_TURN_LIMIT"
)

const (
	ModeIdle         = "idle"
	ModeWorkThenExit = "work-then-exit"
)

type Event struct {
	Time     time.Time         `json:"time"`
	Type     string            `json:"type"`
	Provider string            `json:"provider,omitempty"`
	Mode     string            `json:"mode,omitempty"`
	PID      int               `json:"pid,omitempty"`
	CWD      string            `json:"cwd,omitempty"`
	Argv     []string          `json:"argv,omitempty"`
	Env      map[string]string `json:"env,omitempty"`
	Fields   map[string]string `json:"fields,omitempty"`
	Message  string            `json:"message,omitempty"`
	Error    string            `json:"error,omitempty"`
}

var LaunchEnvKeys = []string{
	"SPORE_TASK_SLUG",
	"SPORE_PROJECT_ROOT",
	"SPORE_TASK_INBOX",
	"SPORE_COORDINATOR_STATE_DIR",
	"WT_PROJECT",
	"WT_SESSION_KIND",
	"SPORE_BRIEF_FILE",
	EnvMode,
	EnvEventLog,
	EnvReadyFile,
	EnvExitFile,
	EnvTranscript,
	EnvTurnLimit,
}
