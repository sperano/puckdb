package player

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"

	"github.com/sperano/puckdb/internal/store"
	"go.temporal.io/sdk/temporal"
)

// ManifestMissingErrorType is the ApplicationError type of a manifest that no
// longer exists or no longer matches the reference a chain carries (the
// database was reset during the chain). It is not retryable: the chain must
// be started again.
const ManifestMissingErrorType = "ProcessPlayersManifestMissing"

// ProcessPlayersManifestRetention is how many manifests (with their players
// and errors) are kept; creating one deletes the older ones.
const ProcessPlayersManifestRetention = 3

// ErrManifestNotFound is returned by a ProcessPlayersStore for a manifest id
// it does not hold.
var ErrManifestNotFound = errors.New("process players manifest not found")

// ManifestRef identifies a durable player manifest: everything a
// ProcessPlayers chain carries about its player list. Hash is the hex sha256
// of the ordered player rows (see hashManifestPlayers).
type ManifestRef struct {
	ID    string
	Count int
	Hash  string
}

// ProcessPlayersStore is the durable storage behind a ProcessPlayers chain:
// its player manifest and per-player errors.
type ProcessPlayersStore interface {
	// GetManifest returns the manifest's reference, or ErrManifestNotFound.
	GetManifest(ctx context.Context, id string) (ManifestRef, error)
	// CreateManifest stores players (already ordered, see sortManifestPlayers)
	// under id in one transaction and deletes manifests beyond the retention.
	// When id already exists it returns the stored manifest unchanged.
	CreateManifest(ctx context.Context, id, workflowID string, players []store.BoxscorePlayer, hash []byte) (ManifestRef, error)
	// ManifestPlayers returns the players at ordinals [from, to), in order.
	ManifestPlayers(ctx context.Context, id string, from, to int) ([]store.BoxscorePlayer, error)
	// RecordErrors upserts errs; ErrManifestNotFound when id does not exist.
	RecordErrors(ctx context.Context, id string, errs []PlayerError) error
	// FinishManifest marks the manifest finished and returns how many errors
	// it holds, or ErrManifestNotFound.
	FinishManifest(ctx context.Context, id string) (int, error)
	// MatchedYahooIDs returns every Yahoo ID a stored player is matched to.
	MatchedYahooIDs(ctx context.Context) ([]store.YahooPlayerID, error)
}

// sortManifestPlayers returns players ordered by player ID, keeping the
// first of any repeated ID, so a manifest's ordinals never depend on the
// order the source list was built in.
func sortManifestPlayers(players []store.BoxscorePlayer) []store.BoxscorePlayer {
	sorted := slices.Clone(players)
	slices.SortStableFunc(sorted, func(a, b store.BoxscorePlayer) int {
		switch {
		case a.ID < b.ID:
			return -1
		case a.ID > b.ID:
			return 1
		default:
			return 0
		}
	})
	return slices.CompactFunc(sorted, func(a, b store.BoxscorePlayer) bool { return a.ID == b.ID })
}

// hashManifestPlayers returns the sha256 of the ordered players: each ID as
// 8 big-endian bytes and each text field prefixed by its 4-byte length, so
// two different field splits never hash the same.
func hashManifestPlayers(players []store.BoxscorePlayer) []byte {
	h := sha256.New()
	var buf [8]byte
	writeText := func(s string) {
		binary.BigEndian.PutUint32(buf[:4], uint32(len(s)))
		h.Write(buf[:4])
		h.Write([]byte(s))
	}
	for _, p := range players {
		binary.BigEndian.PutUint64(buf[:], uint64(p.ID))
		h.Write(buf[:])
		writeText(p.FirstName)
		writeText(p.LastName)
		writeText(p.Position)
	}
	return h.Sum(nil)
}

// newManifestRef builds the reference of a manifest from its stored header.
func newManifestRef(id string, count int, hash []byte) ManifestRef {
	return ManifestRef{ID: id, Count: count, Hash: hex.EncodeToString(hash)}
}

// manifestMissingError is the non-retryable failure of a chain whose
// manifest is gone or changed.
func manifestMissingError(id, reason string) error {
	return temporal.NewNonRetryableApplicationError(
		fmt.Sprintf("manifest %s %s; restart processPlayers", id, reason),
		ManifestMissingErrorType, nil)
}
