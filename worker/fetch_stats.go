package worker

// FetchStats tracks download statistics common to batch fetch operations.
type FetchStats struct {
	Downloaded int // Items downloaded from API/network
	CacheHits  int // Items found in cache
	Missing    int // 404 responses (item doesn't exist)
}

// Add accumulates stats from another FetchStats.
func (f *FetchStats) Add(other FetchStats) {
	f.Downloaded += other.Downloaded
	f.CacheHits += other.CacheHits
	f.Missing += other.Missing
}
