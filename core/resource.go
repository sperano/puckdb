package core

// Resource is the core abstraction representing a storable data resource.
// Every resource knows its local storage path and file type.
type Resource interface {
	// Path returns the storage path for this resource (relative to data root).
	Path() string

	// Type returns the FileType for metrics and logging.
	Type() FileType
}

// RedisKey returns a unique Redis key to identify the given resource.
func RedisKey(r Resource) string {
	return RedisResourceKeyPrefix + r.Path()
}

// URLResource is a Resource that can be fetched from a remote URL.
// Not all resources have URLs (e.g., missing player markers, resources
// fetched via API client methods rather than direct URLs).
type URLResource interface {
	Resource

	// URL returns the remote URL to fetch this resource from.
	URL() string
}

// Parseable is a generic interface for resources that can be deserialized
// into a typed value. This enables type-safe parsing without runtime assertions.
type Parseable[T any] interface {
	Resource

	// Parse deserializes raw bytes into the typed value.
	Parse(data []byte) (T, error)
}

// Formattable is a generic interface for resources that can serialize
// a typed value back to raw bytes. Combined with Parseable, this enables
// round-trip serialization.
type Formattable[T any] interface {
	Resource

	// Format serializes the typed value to raw bytes.
	Format(obj T) ([]byte, error)
}

// ReadWritable combines Parseable and Formattable for full round-trip support.
type ReadWritable[T any] interface {
	Parseable[T]
	Formattable[T]
}
