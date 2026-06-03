// SPDX-License-Identifier: MIT

package opsserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/ops/backup"
)

// TestBackupRegionUnresolvableMapsTo422Permanent_spec_25_11_4336 asserts
// the §25.11 line 4336 mapping: BACKUP_REGION_UNRESOLVABLE is a PERMANENT
// HTTP 422, so an agent treats a residency abort as a configuration fault
// to fix rather than a transient failure to retry.
func TestBackupRegionUnresolvableMapsTo422Permanent_spec_25_11_4336(t *testing.T) {
	rec := httptest.NewRecorder()
	writeBackupError(rec, &backup.Error{
		Code:    backup.ErrCodeBackupRegionUnresolvable,
		Message: "region us-east-1 has no backups.regions entry",
	})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	var resp struct {
		Error struct {
			Code     string `json:"code"`
			Category string `json:"category"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error.Code != backup.ErrCodeBackupRegionUnresolvable {
		t.Errorf("code = %q, want %q", resp.Error.Code, backup.ErrCodeBackupRegionUnresolvable)
	}
	if resp.Error.Category != "PERMANENT" {
		t.Errorf("category = %q, want PERMANENT", resp.Error.Category)
	}
}
