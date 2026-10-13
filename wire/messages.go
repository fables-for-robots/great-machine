package wire

import "time"

// ALPN is the protocol of every iroh connection of great-machine.
const ALPN = "great-machine/1"

// ID is an endpoint ID: 64 lowercase hexadecimal characters. node.ParseID
// makes one out of text; everywhere else an ID is taken as it is.
type ID string

// Request is the first message of a stream. Exactly one field is set.
type Request struct {
	Register *Register        `json:"register,omitempty"`
	Prepare  *Prepare         `json:"prepare,omitempty"`
	Activate *Activate        `json:"activate,omitempty"`
	Logs     *LogsRequest     `json:"logs,omitempty"`
	Shell    *ShellRequest    `json:"shell,omitempty"`
	Store    *StoreRequest    `json:"store,omitempty"`
	Rollout  *RolloutRequest  `json:"rollout,omitempty"`
	Rollback *RollbackRequest `json:"rollback,omitempty"`
	Watch    *WatchRequest    `json:"watch,omitempty"`
	Rollouts *RolloutsRequest `json:"rollouts,omitempty"`
	History  *HistoryRequest  `json:"history,omitempty"`

	// What an operator asks about who may do what, and of the page of the
	// store: access.go.
	Grants    *GrantsRequest    `json:"grants,omitempty"`
	Grant     *GrantRequest     `json:"grant,omitempty"`
	Revoke    *RevokeRequest    `json:"revoke,omitempty"`
	Me        *MeRequest        `json:"me,omitempty"`
	StorePage *StorePageRequest `json:"store_page,omitempty"`
}

// Response is every message that answers a Request. Error, or exactly one
// of the others, is set.
type Response struct {
	Error      *Error       `json:"error,omitempty"`
	Registered *Registered  `json:"registered,omitempty"`
	Prepared   *Prepared    `json:"prepared,omitempty"`
	Activated  *Activated   `json:"activated,omitempty"`
	Line       *LogLine     `json:"line,omitempty"`
	Shell      *ShellOutput `json:"shell,omitempty"`
	Store      *StoreInfo   `json:"store,omitempty"`
	Rollout    *RolloutInfo `json:"rollout,omitempty"`
	Fleet      *Fleet       `json:"fleet,omitempty"`
	Rollouts   *RolloutList `json:"rollouts,omitempty"`
	History    *SampleList  `json:"history,omitempty"`

	Grants    *GrantList `json:"grants,omitempty"`
	Granted   *Granted   `json:"granted,omitempty"`
	Revoked   *Revoked   `json:"revoked,omitempty"`
	Me        *Identity  `json:"me,omitempty"`
	StorePage *StorePage `json:"store_page,omitempty"`
}

// Phase is what a zathras is doing with its service.
type Phase string

// The phases of a zathras.
const (
	// PhaseIdle: the zathras has no generation.
	PhaseIdle Phase = "idle"
	// PhaseRunning: the service runs.
	PhaseRunning Phase = "running"
	// PhaseStopping: the service was told to end and has not ended.
	PhaseStopping Phase = "stopping"
	// PhaseBackoff: the service has ended and waits to be started.
	PhaseBackoff Phase = "backoff"
)

// Exit is how a service ended.
type Exit struct {
	Generation uint64        `json:"generation"`
	Status     int           `json:"status"`           // the exit status; -1 when a signal ended it or it never started
	Signal     string        `json:"signal,omitempty"` // such as "SIGKILL"
	Error      string        `json:"error,omitempty"`  // why the binary could not be started
	At         time.Time     `json:"at"`
	Ran        time.Duration `json:"ran"`
}

// Sample is the figures of a container at one time.
type Sample struct {
	At          time.Time `json:"at"`
	CPU         float64   `json:"cpu"`          // cores
	Memory      uint64    `json:"memory"`       // bytes
	MemoryLimit uint64    `json:"memory_limit"` // bytes; 0 for none
	DiskRead    float64   `json:"disk_read"`    // bytes per second
	DiskWrite   float64   `json:"disk_write"`
	NetRx       float64   `json:"net_rx"`
	NetTx       float64   `json:"net_tx"`
}

// State is what a zathras reports.
type State struct {
	Generation uint64        `json:"generation"` // 0 for none
	Binary     string        `json:"binary,omitempty"`
	Args       []string      `json:"args,omitempty"`
	Phase      Phase         `json:"phase"`
	PID        int           `json:"pid,omitempty"`         // while running
	RunningFor time.Duration `json:"running_for,omitempty"` // while running
	RestartAt  time.Time     `json:"restart_at"`            // while in backoff; the zero time otherwise
	LastExit   *Exit         `json:"last_exit,omitempty"`
	Restarts   uint64        `json:"restarts"`
	Prepared   []uint64      `json:"prepared,omitempty"` // ascending
	Preparing  uint64        `json:"preparing,omitempty"`
	Sample     *Sample       `json:"sample,omitempty"`
}

// A zathras and draal.

// Register is the request of a zathras to be counted among those of its
// service. After Registered it writes a State as a frame of its own for
// every report.
type Register struct {
	Service string `json:"service"`
	Name    string `json:"name"`
	Version string `json:"version"`
	State   State  `json:"state"`
	// Statement is what a role key of the service signed for this
	// zathras. Without one draal refuses the registration.
	Statement *Statement `json:"statement,omitempty"`
}

// Registered answers Register.
type Registered struct{}

// Prepare asks a zathras to make the closure of a generation present.
type Prepare struct {
	Generation uint64   `json:"generation"`
	Binary     string   `json:"binary"`
	Closure    []string `json:"closure"` // names of store paths, without /nix/store/
}

// Prepared answers Prepare when the closure is present.
type Prepared struct{}

// Activate asks a zathras to run a generation in place of what it runs.
type Activate struct {
	Generation uint64   `json:"generation"`
	Binary     string   `json:"binary"`
	Args       []string `json:"args,omitempty"`
}

// Activated answers Activate when the zathras has taken the generation as
// its own.
type Activated struct{}

// LogsRequest asks for the ring. Zathras names the zathras when draal is
// asked, and is empty when a zathras is asked.
type LogsRequest struct {
	Zathras ID   `json:"zathras,omitempty"`
	Follow  bool `json:"follow,omitempty"`
}

// LogLine is a line of the ring. One with Lost set has no text: it says
// that so many lines were lost before the line that follows it.
//
// Text is valid UTF-8. The ring sees to that; a message with a text that
// is not is refused by Read.
type LogLine struct {
	At     time.Time `json:"at"`
	Stream string    `json:"stream"` // "stdout", "stderr" or "zathras"
	Text   string    `json:"text,omitempty"`
	Lost   uint64    `json:"lost,omitempty"`
}

// ShellRequest asks for a shell on a pseudo-terminal of the given size.
// Zathras names the zathras when draal is asked, and is empty when a
// zathras is asked.
type ShellRequest struct {
	Zathras ID     `json:"zathras,omitempty"`
	Cols    uint16 `json:"cols"`
	Rows    uint16 `json:"rows"`
	Term    string `json:"term,omitempty"`
}

// WinSize is the size of a terminal.
type WinSize struct {
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

// ShellInput is what the asking side sends after its ShellRequest.
type ShellInput struct {
	Data   []byte   `json:"data,omitempty"`
	Resize *WinSize `json:"resize,omitempty"`
}

// ShellOutput is what answers. The first one is empty and says that the
// shell runs. The last one has Exit set.
type ShellOutput struct {
	Data []byte `json:"data,omitempty"`
	Exit *int   `json:"exit,omitempty"`
}

// An operator and draal.

// StoreRequest asks where the closures of a service are to be pushed.
type StoreRequest struct {
	Service string `json:"service"`
}

// StoreInfo is where the closures of a service are pushed to: the server,
// which is draal itself, answering jaccard-store's protocol under its own
// endpoint ID, and the prefix of the references a push makes there, which
// is the service's own (StorePrefix).
type StoreInfo struct {
	Server ID     `json:"server"`
	Prefix string `json:"prefix"`
}

// RolloutRequest asks for a rollout of a new generation of a service.
type RolloutRequest struct {
	Service  string   `json:"service"`
	Binary   string   `json:"binary"`
	Args     []string `json:"args,omitempty"`
	Closure  []string `json:"closure"`
	Parallel int      `json:"parallel"`
}

// RollbackRequest asks for a rollout back to the stable generation of a
// service.
type RollbackRequest struct {
	Service  string `json:"service"`
	Parallel int    `json:"parallel"`
}

// RolloutState is how far a rollout is.
type RolloutState string

// The states of a rollout.
const (
	// RolloutPreparing: the zathrases are making the closure present.
	RolloutPreparing RolloutState = "preparing"
	// RolloutActivating: the zathrases are moved to the generation.
	RolloutActivating RolloutState = "activating"
	// RolloutCompleted: every zathras runs the generation.
	RolloutCompleted RolloutState = "completed"
	// RolloutHalted: the rollout was stopped, and Reason says why.
	RolloutHalted RolloutState = "halted"
	// RolloutSuperseded: a newer rollout took its place.
	RolloutSuperseded RolloutState = "superseded"
)

// UnderWay says whether s is preparing or activating.
func (s RolloutState) UnderWay() bool {
	return s == RolloutPreparing || s == RolloutActivating
}

// RolloutInfo is a rollout as draal records it.
type RolloutInfo struct {
	ID         uint64       `json:"id"`
	Service    string       `json:"service"`
	Generation uint64       `json:"generation"`
	Parallel   int          `json:"parallel"`
	State      RolloutState `json:"state"`
	Reason     string       `json:"reason,omitempty"`
	Created    time.Time    `json:"created"`
	CreatedBy  ID           `json:"created_by"`
	Finished   time.Time    `json:"finished"` // the zero time while under way
	// Of the connected zathrases, in a Fleet alone:
	Prepared  int `json:"prepared"`
	Activated int `json:"activated"`
	Awaited   int `json:"awaited"`
}

// WatchRequest asks for the fleet, now and whenever it changes.
type WatchRequest struct{}

// Fleet is every service draal knows, with its zathrases.
type Fleet struct {
	Services []Service `json:"services"` // by name
}

// Service is a service, its last rollout and its connected zathrases.
type Service struct {
	Name      string       `json:"name"`
	Stable    uint64       `json:"stable"`            // 0 for none
	Rollout   *RolloutInfo `json:"rollout,omitempty"` // the last one
	Zathrases []Zathras    `json:"zathrases"`         // by name, then by ID
}

// Zathras is a connected zathras and what it reported last.
type Zathras struct {
	ID        ID        `json:"id"`
	Name      string    `json:"name"`
	Connected time.Time `json:"connected"`
	State     State     `json:"state"`
}

// RolloutsRequest asks for the rollouts of a service.
type RolloutsRequest struct {
	Service string `json:"service"`
}

// RolloutList answers RolloutsRequest.
type RolloutList struct {
	Rollouts []RolloutInfo `json:"rollouts"` // newest first
}

// HistoryRequest asks for the samples kept of a zathras.
type HistoryRequest struct {
	Zathras ID `json:"zathras"`
}

// SampleList answers HistoryRequest.
type SampleList struct {
	Samples []Sample `json:"samples"` // oldest first
}
