// SPDX-License-Identifier: MIT

package stack

import "testing"

// TestGatewayGRPCAddr covers the §4.7 substrate-agnostic gateway↔adapter
// callback address composition Up performs at bring-up. The stack joins the
// launcher's substrate-specific GatewayHost with the gateway gRPC host port
// into the address the controller stamps onto agent pods. The Linux
// child-process launcher returns 127.0.0.1 (k3s and the gateway share the
// host); the Docker-backed launcher returns host.docker.internal (pods run
// inside the Docker VM and reach the host gateway through that alias). The
// function is pure, so the OS branch stays confined to the launcher's
// GatewayHost and the §4.7 pod-spec/adapter business logic above it is
// substrate-agnostic.
//
// spec: §4.7 (the in-cluster adapter dials the gateway at this address
// across the host/Docker boundary), §8.6, §9.1, §17.4.
func TestGatewayGRPCAddr_spec_4_7(t *testing.T) {
	cases := []struct {
		name string
		host string
		port int
		want string
	}{
		{
			name: "linux child-process launcher reaches the host at loopback",
			host: "127.0.0.1",
			port: defaultGatewayGRPCPort,
			want: "127.0.0.1:50061",
		},
		{
			name: "docker-backed launcher reaches the host at the docker alias",
			host: "host.docker.internal",
			port: defaultGatewayGRPCPort,
			want: "host.docker.internal:50061",
		},
		{
			name: "an alternate port is joined faithfully",
			host: "host.docker.internal",
			port: 6000,
			want: "host.docker.internal:6000",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := gatewayGRPCAddr(tc.host, tc.port); got != tc.want {
				t.Errorf("gatewayGRPCAddr(%q, %d) = %q, want %q", tc.host, tc.port, got, tc.want)
			}
		})
	}
}
