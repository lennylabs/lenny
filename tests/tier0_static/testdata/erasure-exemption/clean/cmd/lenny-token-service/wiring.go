// SPDX-License-Identifier: MIT

package main

import tokencache "github.com/lennylabs/lenny/pkg/tokenservice/cache"

type wiring struct {
	accessCache *tokencache.Cache
}

func (w *wiring) build() {
	c, _ := tokencache.New(nil, nil)
	w.accessCache = c
	_ = w.accessCache
}
