// Package canonhash is the byte-level mechanism behind PuckDB's content
// identities: SHA-256, written as lowercase hex.
//
// It only hashes; it does not canonicalize. Each identity's canonical shape
// stays with its caller, which decides what it hashes and how: which fields
// an identity struct names (and their order, which is the JSON field
// order), which slices it sorts first, which fields it drops or marks
// omitempty to keep an older identity, and whether the result carries a
// version prefix. encoding/json sorts map keys, so a map in the hashed
// value is canonical; a slice keeps its order. Changing any of those, or
// switching a caller between JSON, Versioned and Bytes, changes stored
// identities and needs a version bump at the caller.
//
// Error convention: JSON and Versioned return json.Marshal's error
// unchanged (it already names the unsupported value: a NaN or infinite
// float, a channel, a function); every caller wraps it with %w and what it
// was hashing. Nothing here panics.
package canonhash

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// versionSeparator joins a version and the digest in Versioned.
const versionSeparator = ":"

// Bytes is the lowercase hex SHA-256 of data.
func Bytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// JSON is Bytes of v's encoding/json encoding. A caller that also stores the
// encoding marshals it itself and hashes it with Bytes.
func JSON(v any) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return Bytes(data), nil
}

// Versioned is "<version>:" followed by JSON(v). The prefix only labels the
// identity; a caller whose identity must change with the version also puts
// the version inside v.
func Versioned(version string, v any) (string, error) {
	hash, err := JSON(v)
	if err != nil {
		return "", err
	}
	return version + versionSeparator + hash, nil
}
