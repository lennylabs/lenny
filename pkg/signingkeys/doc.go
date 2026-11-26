// SPDX-License-Identifier: MIT

// Package signingkeys holds the signing keys that every gateway replica
// shares, grouped by purpose. A purpose's policy names its algorithm, the
// platform KMS alias that wraps its secrets, its rotation interval, and
// how long a replaced key keeps verifying. The keys are stored wrapped
// (the Store interface; pgstore persists them in platform_signing_keys)
// and are unwrapped only into process memory.
//
// A Ring serves one purpose on one replica. It loads the purpose's keys at
// start, writes the first key when the purpose has none, and then, on a
// fixed refresh interval, writes the next key one publish lead ahead of
// its activation when the active key is due for rotation, deletes the
// keys whose verification time has passed, and reloads the keys. Because
// the publish lead is longer than the refresh interval, every replica has
// loaded a key before any replica signs with it. A unique rotation slot
// per purpose makes concurrent writers of the next key produce one key.
//
// A Ring signs with the newest key whose activation instant has passed
// and verifies with every key it holds. When its last successful reload
// is older than the stale bound (the upload-token overlap window), it
// stops handing out a signing key, so a replica that cannot reach the
// store stops minting while it keeps verifying.
//
// The upload-token purpose is the one purpose served today; pkg/uploadtoken
// signs and verifies its tokens through a Ring.
//
// spec: UPLOAD, SESS
package signingkeys
