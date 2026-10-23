// SPDX-License-Identifier: MIT

package main

func (w *wiring) remember(jti string) {
	_ = w.accessCache.Put(nil, jti, nil)
}
