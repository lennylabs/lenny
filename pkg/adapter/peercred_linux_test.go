// SPDX-License-Identifier: MIT

//go:build linux

package adapter

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestCheckPeerUID(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "s")
	lis, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer lis.Close()

	accepted := make(chan net.Conn, 1)
	go func() {
		if c, err := lis.Accept(); err == nil {
			accepted <- c
		}
	}()
	client, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()
	server := <-accepted
	defer server.Close()

	// The peer is this same test process, so its uid is the current uid.
	self := uint32(os.Getuid())
	if err := checkPeerUID(server, self); err != nil {
		t.Errorf("checkPeerUID for the current uid %d = %v, want nil", self, err)
	}
	if err := checkPeerUID(server, self+1); err == nil {
		t.Error("checkPeerUID accepted a peer whose uid does not match")
	}
}
