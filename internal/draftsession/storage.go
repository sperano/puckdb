package draftsession

import "errors"

const maxDatabaseVersion = uint64(1<<63 - 1)

// ErrDatabaseVersionOverflow means a session version cannot fit in a
// PostgreSQL bigint column.
var ErrDatabaseVersionOverflow = errors.New("draft session version exceeds PostgreSQL bigint")

// DatabaseVersion converts a session version to the signed representation
// used by PostgreSQL bigint columns.
func DatabaseVersion(version uint64) (int64, error) {
	if version > maxDatabaseVersion {
		return 0, ErrDatabaseVersionOverflow
	}
	return int64(version), nil
}
