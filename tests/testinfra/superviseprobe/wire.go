// SPDX-License-Identifier: MIT

// Package superviseprobe is the test-only Standard-level probe runtime that
// the cluster tiers run under the runtime supervisor. cmd/runtimes/
// supervise-probe is its binary and calls Main.
//
// The probe serves sessions through the Go runtime SDK and answers text
// commands sent as session messages: it reports a per-process token (its
// PID and start time), the manifest it read before it dialed the adapter,
// and its environment; it double-forks a daemon, creates System V and POSIX
// IPC objects, plants a symbolic link under the workspace base, makes a
// platform MCP call, and checks whether objects an earlier session created
// still exist. It also carries subcommands a test runs with `kubectl exec`
// in the runtime container, at the agent UID: attacks on the supervisor
// (ptrace, /proc/1 reads, a write under the supervisor mount, a signal to
// PID 1, a squat of the CH-SUPERVISE socket name, and a bounded fork bomb),
// a process listing, a kill of the runtime process, and the command a
// pool's cleanupCommands runs, which reports its identity, environment,
// working directory, and access through the planted link to the probe's
// record server.
//
// The image sets an ENV variable (ImageEnvVar) that the probe's Runtime
// `env` does not, so a test can assert that the supervisor's launch
// environment drops the image ENV. DialDelayEnvVar delays the CH-MSGSOCK
// dial of the pod's warm-up launch, the first launch in the pod, so a test
// can act on a pod during that launch; a later launch in the same pod
// dials without the delay.
//
// spec: §4.7.10 (Runtime supervisor); §4.7.11 item 8; §5.2 (Supervised scrub
// sequence); §28.5.3 (CH-SUPERVISE).
package superviseprobe

// Command names a probe accepts as the first word of a session message.
const (
	// CmdReport answers with the probe's Record.
	CmdReport = "report"
	// CmdDaemon double-forks a daemon in its own session and answers with
	// its PID.
	CmdDaemon = "daemon"
	// CmdIPC creates a System V shared-memory segment, a System V message
	// queue, and a POSIX message queue, and answers with their identifiers.
	CmdIPC = "ipc"
	// CmdSymlink plants a symbolic link under the workspace base that points
	// at the adapter's /run/lenny directory.
	CmdSymlink = "symlink"
	// CmdMCP makes one platform MCP call with the manifest nonce.
	CmdMCP = "mcp"
	// CmdResidue takes a JSON Residue and answers whether each object still
	// exists.
	CmdResidue = "residue"
)

// Subcommands of the probe binary.
const (
	// SubAttack runs one attack and prints an AttackResult.
	SubAttack = "attack"
	// SubCleanupRecord is the cleanupCommands entry: it reports a
	// CleanupRecord to the record server.
	SubCleanupRecord = "cleanup-record"
	// SubProcesses prints the container's processes as a JSON array of
	// Process.
	SubProcesses = "processes"
	// SubKillRuntime sends SIGKILL to the serving probe process.
	SubKillRuntime = "kill-runtime"
	// SubList prints the entry names of the directory its argument names as
	// a JSON array, or an AttackResult when it cannot read it.
	SubList = "list"
	// SubSetupResidue is a setup command's entry: run in the adapter
	// container, it double-forks a daemon in its own session and creates a
	// System V shared-memory segment under the key its argument names, the
	// residue the supervised scrub removes from the adapter container, and
	// prints a SetupResidue.
	SubSetupResidue = "setup-residue"
	// SubSetupResidueCheck prints a SetupResidue naming the daemons in the
	// container it runs in and whether the segment under the key its
	// argument names still exists.
	SubSetupResidueCheck = "setup-residue-check"
	// SubSupervisePeer accepts the supervisor's CH-SUPERVISE connection
	// through the adapter's own client and pings it, printing PeerEvent
	// lines, on a pod a test builds outside the platform.
	SubSupervisePeer = "supervise-peer"
	// SubDaemonChild and subDaemonGrandchild are the two halves of the
	// double fork CmdDaemon starts. SubDaemonChild is also a cleanup
	// command's entry on a `restart` pool: it leaves a background process
	// that the second end and sweep of the supervised scrub must remove
	// before the next session.
	SubDaemonChild      = "daemon-child"
	subDaemonGrandchild = "daemon-grandchild"
)

// Attack names, the argument of SubAttack.
const (
	AttackPtracePID1      = "ptrace-pid1"
	AttackReadProc1Mem    = "read-proc1-mem"
	AttackReadProc1FD     = "read-proc1-fd"
	AttackWriteSupervisor = "write-supervisor-mount"
	AttackSignalPID1      = "signal-pid1"
	AttackBindSupervise   = "bind-supervise"
	AttackForkBomb        = "fork-bomb"
)

// Environment variables the probe reads, and the one its image sets. None
// begins with LENNY_, which §5.1 reserves for the platform's own variables
// and the Runtime CRD refuses in `env`.
const (
	// DialDelayEnvVar is a Go duration the pod's first probe process, the
	// warm-up launch, sleeps before it dials CH-MSGSOCK. A later process in
	// the same pod finds LaunchMarker and dials at once. The Kind pools set
	// it through the Runtime `env`.
	DialDelayEnvVar = "PROBE_DIAL_DELAY"
	// RuntimeEnvVar is the variable the Kind probe Runtimes set in `env`.
	RuntimeEnvVar = "PROBE_RUNTIME_ENV"
	// ImageEnvVar is the variable the probe image sets with ENV and no
	// Runtime `env` sets, so the supervisor's launch environment must not
	// carry it.
	ImageEnvVar = "PROBE_IMAGE_ENV"
	// manifestEnvVar overrides the adapter manifest path.
	manifestEnvVar = "LENNY_ADAPTER_MANIFEST"
)

// SignalLogPrefix opens the runtime-container log line the serving probe
// writes for each signal it receives, followed by the signal's name as Go
// prints it (for example "interrupt" for SIGINT).
const SignalLogPrefix = "received signal"

// Fixed paths and names.
const (
	// RecordSocket is the abstract socket of the probe's record server. It
	// lives in the pod's network namespace, so a cleanup command reaches the
	// serving probe on a `keep` pod, whose runtime process outlives the
	// scrub.
	RecordSocket = "@lenny-probe-record"
	// SuperviseSocket is the abstract socket the adapter listens on for the
	// supervisor's CH-SUPERVISE connection. spec: §28.5.3 (CH-SUPERVISE,
	// Endpoint).
	SuperviseSocket = "@lenny-supervise"
	// SupervisorMount is where the runtime container mounts the supervisor
	// volume read-only. spec: §13.1 (Runtime supervisor volume).
	SupervisorMount = "/lenny-supervisor"
	// WorkspaceBase is the workspace volume's mount point. spec: §6.4.
	WorkspaceBase = "/workspace"
	// AdapterRunDir is the adapter's /run/lenny directory the planted link
	// points at.
	AdapterRunDir = "/run/lenny"
	// defaultManifestPath is the adapter manifest path. spec: §4.7.6.
	defaultManifestPath = "/run/lenny/adapter-manifest.json"
	// LaunchMarker is the file the pod's first delayed probe process
	// creates on the pod-scoped /tmp volume, so a later launch in the same
	// pod skips the dial delay.
	LaunchMarker = "/tmp/lenny-probe-launched"
	// mqName is the POSIX message queue CmdIPC creates.
	mqName = "lenny-probe-mq"
)

// ManifestView is what the probe read from the adapter manifest. The nonce
// is reported as a SHA-256 prefix rather than in clear.
type ManifestView struct {
	// Present reports whether the manifest file existed and parsed.
	Present bool `json:"present"`
	// NonceDigest is the first 16 hex digits of the SHA-256 of mcpNonce.
	NonceDigest string `json:"nonceDigest,omitempty"`
	// PlatformMCP reports whether the manifest names a platform MCP socket.
	PlatformMCP bool `json:"platformMcp"`
	// Error is the read or parse failure when Present is false.
	Error string `json:"error,omitempty"`
}

// Record is the probe's answer to CmdReport.
type Record struct {
	// Token is "PID:STARTTIME", unique to one process for the life of the
	// PID namespace.
	Token string `json:"token"`
	// PID is the probe's PID in the container's PID namespace.
	PID int `json:"pid"`
	// ManifestAtStart is the manifest the probe read before its dial.
	ManifestAtStart ManifestView `json:"manifestAtStart"`
	// ManifestNow is the manifest at the time of the report.
	ManifestNow ManifestView `json:"manifestNow"`
	// Env is the probe's environment at start, as NAME=VALUE pairs.
	Env []string `json:"env"`
	// DialDelay is the delay the probe slept before its dial, empty for a
	// process that dialed at once.
	DialDelay string `json:"dialDelay,omitempty"`
	// Cleanups are the records cleanup commands reported to this process.
	Cleanups []CleanupRecord `json:"cleanups"`
	// Signals counts the signals the probe received, by name. The
	// supervisor forwards no signal to the runtime process, so a signal
	// sent to PID 1 must not appear here. spec: §4.7.10 (Runtime
	// supervisor, Signals).
	Signals map[string]int `json:"signals"`
	// RecordServerError is the record server's bind failure, if any.
	RecordServerError string `json:"recordServerError,omitempty"`
}

// CleanupRecord is what a cleanup command reports about itself.
type CleanupRecord struct {
	UID  int      `json:"uid"`
	GID  int      `json:"gid"`
	Cwd  string   `json:"cwd"`
	Env  []string `json:"env"`
	Link []Access `json:"link"`
}

// Access is the outcome of one access attempt through a planted link or
// directly at its target.
type Access struct {
	Path  string `json:"path"`
	Op    string `json:"op"`
	OK    bool   `json:"ok"`
	Errno string `json:"errno,omitempty"`
}

// ActionResult is the probe's answer to CmdDaemon, CmdIPC, CmdSymlink, and
// CmdMCP.
type ActionResult struct {
	OK      bool    `json:"ok"`
	Error   string  `json:"error,omitempty"`
	Residue Residue `json:"residue,omitempty"`
	// Direct is the probe's own access attempts at the link target, which a
	// cleanup command's attempts through the link must not exceed.
	Direct []Access `json:"direct,omitempty"`
}

// Residue names objects one session created, for a later session's
// CmdResidue. A zero or nil field is not checked; the IPC identifiers are
// pointers because 0 is a valid System V identifier.
type Residue struct {
	DaemonPID int    `json:"daemonPid,omitempty"`
	ShmID     *int   `json:"shmId,omitempty"`
	MsqID     *int   `json:"msqId,omitempty"`
	MQ        string `json:"mq,omitempty"`
	Link      string `json:"link,omitempty"`
}

// ResidueResult reports which objects of a Residue still exist.
type ResidueResult struct {
	DaemonAlive bool `json:"daemonAlive"`
	ShmExists   bool `json:"shmExists"`
	MsqExists   bool `json:"msqExists"`
	MQExists    bool `json:"mqExists"`
	LinkExists  bool `json:"linkExists"`
}

// AttackResult is what SubAttack prints.
type AttackResult struct {
	Attack string `json:"attack"`
	// OK reports whether the operation succeeded. For every attack but
	// AttackBindSupervise and AttackSignalPID1 a success is a breach.
	OK     bool   `json:"ok"`
	Errno  string `json:"errno,omitempty"`
	Detail string `json:"detail,omitempty"`
	// Connections counts the connections a squatted socket accepted.
	Connections int `json:"connections,omitempty"`
}

// Process is one entry of SubProcesses.
type Process struct {
	PID     int      `json:"pid"`
	PPID    int      `json:"ppid"`
	UID     int      `json:"uid"`
	State   string   `json:"state"`
	Cmdline []string `json:"cmdline"`
}

// SetupResidue is what SubSetupResidue and SubSetupResidueCheck print: the
// PIDs of the probe daemons in the container and whether the System V
// segment under Key exists.
type SetupResidue struct {
	Key        int    `json:"key"`
	DaemonPIDs []int  `json:"daemonPids"`
	ShmExists  bool   `json:"shmExists"`
	Error      string `json:"error,omitempty"`
}
