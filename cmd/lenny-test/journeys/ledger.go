// SPDX-License-Identifier: MIT

package journeys

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/testtmp"
)

// Default spending caps from decision 5 of change 0157: $6 for one run
// and $54 for the pilot's nine runs. Both are flags of the journeys tier.
const (
	DefaultRunCapUSD   = 6.0
	DefaultPilotCapUSD = 54.0
)

// ErrPilotBudget is returned when starting a run could take the pilot's
// spending past its cap.
var ErrPilotBudget = errors.New("pilot budget")

// Ledger records what each run of one pilot spent, in a JSON file in the
// host-wide journeys state directory (DefaultStateDir). Before a run
// starts, the harness checks that the pilot's recorded spending plus the
// most the run may spend stays within the pilot's cap, refuses the run
// otherwise, and records a pending entry charged at that most. The entry
// is settled at the run's cost when the run ends. A pending entry a dead
// process left stays charged, so a crash never frees budget the crashed
// run may have spent. Agent cost and infrastructure cost are separate
// lines of each entry, so a target that costs money, such as a cloud
// cluster, counts against the same cap.
type Ledger struct {
	Pilot   string        `json:"pilot"`
	CapUSD  float64       `json:"capUSD"`
	Entries []LedgerEntry `json:"entries"`

	path string
}

// LedgerEntry is one run: pending while it runs, settled when it ends.
type LedgerEntry struct {
	// ID identifies the run within the ledger: the run identifier, the
	// journey, and the trial.
	ID      string `json:"id"`
	Run     string `json:"run"`
	Journey string `json:"journey"`
	Trial   int    `json:"trial"`
	Target  string `json:"target"`
	// AgentUSD is what the journey agent spent: Claude Code's reported
	// total, or the harness's estimate when it stopped the agent before
	// the agent reported one.
	AgentUSD          float64 `json:"agentUSD"`
	AgentCostReported bool    `json:"agentCostReported"`
	// InfraUSD is what the cluster target reports the run's
	// infrastructure cost; zero for Kind.
	InfraUSD float64   `json:"infraUSD"`
	Verdict  string    `json:"verdict"`
	At       time.Time `json:"at"`
	// Pending marks an entry reserved before its run and not yet
	// settled; its cost lines hold the most the run may spend. PID is the
	// process that reserved it.
	Pending bool `json:"pending,omitempty"`
	PID     int  `json:"pid,omitempty"`
}

// EntryID returns the ledger identifier of one trial of a run.
func EntryID(run, journey string, trial int) string {
	return fmt.Sprintf("%s/%s/%d", run, journey, trial)
}

// DefaultStateDir returns the host-wide directory that holds the journeys
// lock and the pilot ledgers, $HOME/.lenny-tmp/journeys. It is one place
// per host, so two checkouts or two results directories still share one
// lock and one ledger per pilot.
func DefaultStateDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", fmt.Errorf("no home directory for the journeys state: %v", err)
	}
	return filepath.Join(home, testtmp.HomeDirName, "journeys"), nil
}

// LedgerPath returns the ledger file of the named pilot under dir.
func LedgerPath(dir, pilot string) string {
	return filepath.Join(dir, "ledger-"+pilot+".json")
}

// OpenLedger reads the pilot's ledger at path, or starts an empty one
// with capUSD when the file does not exist. A ledger keeps the cap it was
// started with: opening it with a different cap is an error, so a cap
// cannot be raised for a pilot part-way through it. A new pilot name
// starts a new ledger.
func OpenLedger(path, pilot string, capUSD float64) (*Ledger, error) {
	if capUSD <= 0 {
		return nil, fmt.Errorf("pilot cap $%.2f is not positive", capUSD)
	}
	l := &Ledger{Pilot: pilot, CapUSD: capUSD, path: path}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return l, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, l); err != nil {
		return nil, fmt.Errorf("ledger %s: %w", path, err)
	}
	l.path = path
	if l.Pilot != pilot {
		return nil, fmt.Errorf("ledger %s belongs to pilot %q, not %q", path, l.Pilot, pilot)
	}
	if l.CapUSD != capUSD {
		return nil, fmt.Errorf("ledger %s records a pilot cap of $%.2f; the run asked for $%.2f. Name a new pilot with --pilot to use another cap", path, l.CapUSD, capUSD)
	}
	return l, nil
}

// Spent is the pilot's recorded spending, agent and infrastructure,
// with each pending entry charged at the most its run may spend.
func (l *Ledger) Spent() float64 {
	total := 0.0
	for _, e := range l.Entries {
		total += e.AgentUSD + e.InfraUSD
	}
	return total
}

// CheckStart refuses a run whose most possible spending, maxRunUSD,
// would take the pilot past its cap.
func (l *Ledger) CheckStart(maxRunUSD float64) error {
	const epsilon = 1e-9
	if spent := l.Spent(); spent+maxRunUSD > l.CapUSD+epsilon {
		return fmt.Errorf("%w: pilot %q has spent $%.4f of $%.2f, and a run may spend up to $%.2f, which would exceed the cap",
			ErrPilotBudget, l.Pilot, spent, l.CapUSD, maxRunUSD)
	}
	return nil
}

// Record appends an entry and writes the ledger.
func (l *Ledger) Record(e LedgerEntry) error {
	l.Entries = append(l.Entries, e)
	return l.save()
}

// Reserve checks that a run may start and records its pending entry,
// charged at agentMaxUSD and infraMaxUSD, before the run spends anything.
func (l *Ledger) Reserve(e LedgerEntry, agentMaxUSD, infraMaxUSD float64) error {
	if e.ID == "" {
		return errors.New("a ledger reservation needs an ID")
	}
	if err := l.CheckStart(agentMaxUSD + infraMaxUSD); err != nil {
		return err
	}
	e.Pending, e.PID = true, os.Getpid()
	e.AgentUSD, e.InfraUSD = agentMaxUSD, infraMaxUSD
	return l.Record(e)
}

// Settle replaces the pending entry with e's ID by e, which carries the
// run's cost, and writes the ledger. Settling an ID that has no pending
// entry appends e, so a run's cost is never lost.
func (l *Ledger) Settle(e LedgerEntry) error {
	e.Pending, e.PID = false, 0
	for i := range l.Entries {
		if l.Entries[i].ID == e.ID && l.Entries[i].Pending {
			l.Entries[i] = e
			return l.save()
		}
	}
	return l.Record(e)
}

// PendingEntries splits the entries still pending by whether the process
// that reserved each is alive. Both kinds stay charged; the split only
// tells the operator which runs crashed.
func (l *Ledger) PendingEntries() (live, dead []LedgerEntry) {
	for _, e := range l.Entries {
		if !e.Pending {
			continue
		}
		if processAlive(e.PID) {
			live = append(live, e)
		} else {
			dead = append(dead, e)
		}
	}
	return live, dead
}

func (l *Ledger) save() error {
	if err := os.MkdirAll(filepath.Dir(l.path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(l.path, append(b, '\n'))
}

func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Lock is held for a whole journeys run, and for a probe. Every trial
// uses the one cluster name lenny-journeys (decision 6), and the ledger
// is read and written during a run, so two journeys processes on one
// host must not overlap. The lock lives in the host-wide state directory
// with the ledgers, whatever --results-dir names.
type Lock struct{ path string }

// AcquireLock creates dir/journeys.lock holding this process's ID. A lock
// left by a process that no longer runs is taken over.
func AcquireLock(dir string) (*Lock, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "journeys.lock")
	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_, werr := f.WriteString(strconv.Itoa(os.Getpid()) + "\n")
			cerr := f.Close()
			if werr != nil || cerr != nil {
				_ = os.Remove(path)
				return nil, errors.Join(werr, cerr)
			}
			return &Lock{path: path}, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		b, rerr := os.ReadFile(path)
		pid, perr := strconv.Atoi(strings.TrimSpace(string(b)))
		if rerr == nil && perr == nil && processAlive(pid) {
			return nil, fmt.Errorf("another journeys run (process %d) holds %s", pid, path)
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("could not acquire %s", path)
}

// Release removes the lock file.
func (l *Lock) Release() error {
	if err := os.Remove(l.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}
