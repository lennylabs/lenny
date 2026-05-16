// SPDX-License-Identifier: MIT

// Package credfile materializes the §4.7 runtime credential file. The
// adapter writes the session's credential leases to a tmpfs-backed
// credentials.json so the runtime reads credentials from a file rather
// than from environment variables, the control socket, or an MCP
// server.
package credfile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

const (
	// FileName is the credential file the runtime reads (§4.7).
	FileName = "credentials.json"
	// FileMode is the credential file's permission bits: read for the
	// owning adapter UID and the lenny-cred-readers group, no access for
	// other UIDs (§13.1). Group ownership is set by the pod's fsGroup at
	// volume mount time, so the adapter performs no chown.
	FileMode os.FileMode = 0o440
)

// credentialDocument is the top-level structure of the credential file:
// a JSON object with a providers array, present even for one provider
// so the runtime iterates a single code path (§4.7).
type credentialDocument struct {
	Providers []json.RawMessage `json:"providers"`
}

// Write materializes the credential file in dir from the session's
// credential leases (§4.7 item 4). The write goes to a temporary file
// in dir that is renamed into place, so a concurrent reader never
// observes a partial or empty file. Leases are written in the order
// given; the caller orders them for a deterministic file.
func Write(dir string, leases []*adapterv1.CredentialLease) error {
	entries := make([]json.RawMessage, 0, len(leases))
	for _, lease := range leases {
		entry, err := entryDocument(lease)
		if err != nil {
			return err
		}
		entries = append(entries, entry)
	}
	data, err := json.MarshalIndent(credentialDocument{Providers: entries}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode credential file: %w", err)
	}
	return writeAtomic(filepath.Join(dir, FileName), data)
}

// entryDocument builds one providers-array entry: the lease's typed
// fields (leaseId, provider, expiresAt) merged over the provider's
// opaque payload object (deliveryMode, materializedConfig).
func entryDocument(lease *adapterv1.CredentialLease) (json.RawMessage, error) {
	entry := map[string]json.RawMessage{}
	if payload := lease.GetPayload(); len(payload) > 0 {
		if err := json.Unmarshal(payload, &entry); err != nil {
			return nil, fmt.Errorf("credential lease %s: payload is not a JSON object: %w",
				lease.GetLeaseId(), err)
		}
	}
	putString(entry, "leaseId", lease.GetLeaseId())
	putString(entry, "provider", lease.GetProvider())
	if ms := lease.GetExpiresAtUnixMs(); ms > 0 {
		putString(entry, "expiresAt", time.UnixMilli(ms).UTC().Format(time.RFC3339))
	}
	return json.Marshal(entry)
}

// putString sets a JSON string value on the entry map, skipping empty
// values so absent typed fields do not appear in the file.
func putString(entry map[string]json.RawMessage, key, value string) {
	if value == "" {
		return
	}
	raw, _ := json.Marshal(value)
	entry[key] = raw
}

// writeAtomic writes data to path through a temporary file that is
// renamed into place at mode FileMode.
func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".credentials-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp credential file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp credential file: %w", err)
	}
	if err := tmp.Chmod(FileMode); err != nil {
		tmp.Close()
		return fmt.Errorf("set credential file mode: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp credential file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename credential file into place: %w", err)
	}
	return nil
}
