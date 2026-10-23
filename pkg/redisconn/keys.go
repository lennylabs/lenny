// SPDX-License-Identifier: MIT

package redisconn

import (
	"fmt"
	"strconv"

	"github.com/redis/go-redis/v9"
)

// keylessCommands carry no key in argument position 1 (they carry a cursor,
// a subcommand, or nothing there). CommandKeys returns no keys for them.
var keylessCommands = map[string]struct{}{
	"ping": {}, "echo": {}, "select": {}, "auth": {}, "hello": {},
	"scan": {}, "cluster": {}, "info": {}, "client": {}, "command": {},
	"dbsize": {}, "flushdb": {}, "flushall": {}, "script": {}, "wait": {},
	"multi": {}, "exec": {}, "discard": {}, "unwatch": {}, "swapdb": {},
}

// multiKeyCommands take an unbounded run of keys starting at argument 1.
var multiKeyCommands = map[string]struct{}{
	"del": {}, "unlink": {}, "mget": {}, "watch": {}, "exists": {},
	"touch": {}, "sunion": {}, "sinter": {}, "sdiff": {}, "pfcount": {},
	"pfmerge": {}, "sunionstore": {}, "sinterstore": {}, "sdiffstore": {},
}

// twoKeyCommands name a source key at argument 1 and a destination key at
// argument 2; any later arguments are not keys.
var twoKeyCommands = map[string]struct{}{
	"rename": {}, "renamenx": {}, "smove": {}, "rpoplpush": {},
	"brpoplpush": {}, "lmove": {}, "blmove": {}, "copy": {},
}

// CommandKeys returns the key arguments of cmd, as Redis Cluster would hash
// them to route the command: the declared KEYS of a script or function
// call, every key of a multi-key command, the source and destination of a
// two-key command, and argument 1 of any other keyed command. A keyless
// command (PING, SCAN, MULTI, EXEC, etc.) returns nil. A pub/sub channel
// in argument 1 is returned as a key, so the tenant guard validates it.
//
// The tenant-isolation guard (`rediskeys.Guard`) validates these keys
// against the calling tenant's scope, and the cross-slot test hook
// (`slotguard.Hook`) checks that they share one hash slot, so both read a
// command's keys the same way.
func CommandKeys(cmd redis.Cmder) []string {
	args := cmd.Args()
	if len(args) < 2 {
		return nil
	}
	name := cmd.Name()
	if _, ok := keylessCommands[name]; ok {
		return nil
	}
	switch name {
	case "eval", "evalsha", "eval_ro", "evalsha_ro", "fcall", "fcall_ro":
		// [name, script|sha|fn, numkeys, key1, key2, ..., arg1, ...]
		if len(args) < 3 {
			return nil
		}
		n := toInt(args[2])
		keys := make([]string, 0, n)
		for i := 0; i < n && 3+i < len(args); i++ {
			keys = append(keys, toString(args[3+i]))
		}
		return keys
	case "mset", "msetnx":
		// [name, key1, val1, key2, val2, ...]
		keys := make([]string, 0, (len(args)-1)/2)
		for i := 1; i < len(args); i += 2 {
			keys = append(keys, toString(args[i]))
		}
		return keys
	}
	if _, ok := multiKeyCommands[name]; ok {
		keys := make([]string, 0, len(args)-1)
		for i := 1; i < len(args); i++ {
			keys = append(keys, toString(args[i]))
		}
		return keys
	}
	if _, ok := twoKeyCommands[name]; ok && len(args) >= 3 {
		return []string{toString(args[1]), toString(args[2])}
	}
	return []string{toString(args[1])}
}

func toString(v interface{}) string {
	switch s := v.(type) {
	case string:
		return s
	case []byte:
		return string(s)
	default:
		return fmt.Sprint(v)
	}
}

func toInt(v interface{}) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case string:
		i, _ := strconv.Atoi(n)
		return i
	default:
		i, _ := strconv.Atoi(fmt.Sprint(v))
		return i
	}
}
