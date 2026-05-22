// SPDX-License-Identifier: MIT

package exec

import "os"

func osReadFile(p string) ([]byte, error) { return os.ReadFile(p) }
