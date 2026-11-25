package cache

import (
	"github.com/go-redis/redismock/v9"
	"github.com/redis/go-redis/v9"
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
	redisLockValueArgumentIndex = 4
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
	lockObtainCmd(mock, anyArgsMatch).SetVal(int64(1))
}

// expectLockObtainRecording is expectLockObtain that records the wire arguments in dst.
func expectLockObtainRecording(mock redismock.ClientMock, dst *[]any) {
	lockObtainCmd(mock, recordingMatch(dst)).SetVal(int64(1))
}

// expectLockContended expects an obtain attempt that finds the lock held.
func expectLockContended(mock redismock.ClientMock) {
	lockObtainCmd(mock, anyArgsMatch).SetErr(redis.Nil)
}

// expectLockObtainError expects an obtain attempt that fails with err.
func expectLockObtainError(mock redismock.ClientMock, err error) {
	lockObtainCmd(mock, anyArgsMatch).SetErr(err)
}

// expectLockRelease expects one successful lock release.
func expectLockRelease(mock redismock.ClientMock) {
	lockReleaseCmd(mock, anyArgsMatch).SetVal(int64(1))
}

// expectLockReleaseRecording is expectLockRelease that records the wire arguments in dst.
func expectLockReleaseRecording(mock redismock.ClientMock, dst *[]any) {
	lockReleaseCmd(mock, recordingMatch(dst)).SetVal(int64(1))
}

// expectLockReleaseError expects a release that fails with err.
func expectLockReleaseError(mock redismock.ClientMock, err error) {
	lockReleaseCmd(mock, anyArgsMatch).SetErr(err)
}
