package yahoo

import (
	"github.com/go-redis/redismock/v9"
)

// redislock v0.10 obtains a lock with
//
//	EVALSHA <sha> 1 <key> <value> <tokenLen> <ttlMs>
//
// and releases it with EVALSHA <sha> 1 <key> <value>. redismock needs
// expected arguments to build a command, so the placeholders below only fix
// the command's shape; the matcher decides what is accepted.
const (
	lockPlaceholderSHA      = "any"
	lockPlaceholderKey      = "any"
	lockPlaceholderValue    = "x"
	lockPlaceholderTokenLen = 0
	lockPlaceholderTTL      = "ttl"

	// Positions in the wire arguments of the obtain script.
	obtainScriptArgCount = 7 // EVALSHA sha numkeys key value tokenLen ttlMs
	obtainScriptKeyIndex = 3
)

type argsMatcher = func(expected, actual []any) error

func lockObtainCmd(mock redismock.ClientMock, match argsMatcher) *redismock.ExpectedCmd {
	return mock.CustomMatch(match).ExpectEvalSha(lockPlaceholderSHA, []string{lockPlaceholderKey},
		lockPlaceholderValue, lockPlaceholderTokenLen, lockPlaceholderTTL)
}

func lockReleaseCmd(mock redismock.ClientMock, match argsMatcher) *redismock.ExpectedCmd {
	return mock.CustomMatch(match).ExpectEvalSha(lockPlaceholderSHA, []string{lockPlaceholderKey},
		lockPlaceholderValue)
}

// expectLockObtain expects one successful lock obtain.
func expectLockObtain(mock redismock.ClientMock) {
	lockObtainCmd(mock, anyArgs).SetVal(int64(1))
}

// expectLockRelease expects one successful lock release.
func expectLockRelease(mock redismock.ClientMock) {
	lockReleaseCmd(mock, anyArgs).SetVal(int64(1))
}
