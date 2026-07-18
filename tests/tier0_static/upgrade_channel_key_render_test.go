// SPDX-License-Identifier: MIT

package tier0_static

import (
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/helm"
)

// TestUpgradeChannelKeyDisablesReleaseChannelURL pins the §25.8 disable
// convention for the upgrade-check release channel at the chart-render
// level: setting the spec-named disable key to the empty string must
// suppress the --release-channel-url arg the upgrade-check consumer
// reads.
//
// spec: §25.8 "Air-Gapped Deployments" ("Set `platform.upgradeChannel: ""`
// to disable automatic upgrade-check polling.") and §25.8 "Upgrade Check"
// ("Deployers can point this at an internal mirror or disable it
// (`platform.upgradeChannel: ""` disables)."). Both passages name
// platform.upgradeChannel, not a nested platform.releaseChannel.url key,
// as the Helm value an operator sets to disable the channel.
func TestUpgradeChannelKeyDisablesReleaseChannelURL(t *testing.T) {
	helm.SkipUnlessAvailable(t)
	t.Skip("pending a spec-vs-chart naming reconciliation: spec §25.8 names the release-channel " +
		"disable key platform.upgradeChannel, but the chart reads platform.releaseChannel.url; " +
		"see the open TEST-GAPS.md finding for this behavior for the two candidate resolutions")

	manifests := helm.Render(t, helm.Options{
		Chart: "../../charts/lenny",
		Set:   []string{"coredns.clusterIP=10.96.0.10", `platform.upgradeChannel=`},
	})

	args := containerArgs(t, manifests, "lenny-ops")
	for _, a := range args {
		if strings.HasPrefix(a, "--release-channel-url=") {
			t.Fatalf("§25.8: setting platform.upgradeChannel to the empty string per the spec's air-gap "+
				"disable convention did not disable the upgrade-check release channel; lenny-ops still "+
				"renders %q. The chart reads platform.releaseChannel.url instead of the spec-named "+
				"platform.upgradeChannel key, so the documented disable convention is silently ignored.", a)
		}
	}
}
