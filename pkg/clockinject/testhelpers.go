// SPDX-License-Identifier: MIT

package clockinject

import "os"

// lookupEnvForTest mirrors os.LookupEnv as a tiny indirection so
// the test file does not import os just for one call.
func lookupEnvForTest(name string) (string, bool) { return os.LookupEnv(name) }

// unsetEnvForTest mirrors os.Unsetenv.
func unsetEnvForTest(name string) error { return os.Unsetenv(name) }

// setEnvForTest mirrors os.Setenv.
func setEnvForTest(name, value string) error { return os.Setenv(name, value) }
