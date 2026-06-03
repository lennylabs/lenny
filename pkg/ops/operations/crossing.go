// SPDX-License-Identifier: MIT

package operations

// ProgressThresholds is the §25.2 line 401 set of named percent
// thresholds. The event stream emits an operation_progressed event
// whenever an operation's percent crosses one of these on the way up.
var ProgressThresholds = []int{10, 25, 50, 75, 90, 95, 99}

// CrossedThresholds returns the §25.2 named percent thresholds that lie
// strictly above prev and at or below cur — the thresholds an operation
// crossed when its percent advanced from prev to cur. A non-advancing
// or backward step (cur <= prev) crosses nothing. prev < 0 marks "no
// prior reading", so the first observation reports every threshold at or
// below cur.
//
// spec: §25.2 line 401 (percent crosses a named threshold: 10, 25, 50,
// 75, 90, 95, 99).
func CrossedThresholds(prev, cur float64) []int {
	if cur <= prev {
		return nil
	}
	var crossed []int
	for _, t := range ProgressThresholds {
		ft := float64(t)
		if ft > prev && ft <= cur {
			crossed = append(crossed, t)
		}
	}
	return crossed
}
