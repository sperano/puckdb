package cmd

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/store"
)

// pathIndex is a precomputed view of the on-disk cache. Building it costs one
// pass over dataPath; afterwards, "does this cached file exist?" collapses to
// an O(1) map lookup instead of a fresh os.Stat.
//
// Why this exists: the cache metrics pipeline previously did three independent
// FUSE-Stat passes over the same files (per-season Exists checks, asset Exists
// checks, and a full WalkDir for size). On JuiceFS each Stat is an RPC, so
// hundreds of thousands of round-trips dominated wall time. Building this
// index once and reusing it for the rest of the tick collapses those passes.
type pathIndex struct {
	present    map[string]struct{}
	byType     map[core.FileType]*fileTypeStats
	totalBytes int64
}

// has reports whether dataPath/relPath was seen during the walk. relPath is
// normalised to forward slashes to match the keys produced by buildPathIndex,
// since Storage.Exists callers construct paths with filepath.Join (which uses
// the OS separator) but resource.Path implementations use forward slashes.
func (p *pathIndex) has(relPath string) bool {
	if p == nil || len(p.present) == 0 {
		return false
	}
	_, ok := p.present[filepath.ToSlash(relPath)]
	return ok
}

// indexWalkConcurrency caps the number of in-flight subtree walks. Disjoint
// subtrees hit different metadata shards on JuiceFS, so a moderate number of
// concurrent walks keeps the FUSE pipeline full without piling up so many
// goroutines that scheduler overhead dominates.
const indexWalkConcurrency = 16

// indexFanoutDepth controls how deep we recurse before launching parallel
// subtree walks. A naive top-level fan-out would single-thread the entire
// seasons/{year}/... subtree (where the bulk of the cache lives); fanning out
// at depth=2 instead spawns one walker per (top-level, second-level) pair —
// so seasons/2024, seasons/2023, assets/players, edge/skaters, etc. all run
// concurrently. Files encountered at shallower depths are stat'd inline.
const indexFanoutDepth = 2

// buildPathIndex walks dataPath in parallel — one bounded worker pool over
// top-level subtrees — recording every file path and computing per-type
// counts/bytes in a single pass.
//
// Subtree-level parallelism is the right granularity for JuiceFS: walking
// disjoint subtrees lets multiple metadata RPCs be in flight at once, and the
// merge step is a trivial map union under a single mutex. Within one subtree
// we still rely on filepath.WalkDir, which is sequential — that matches what
// JuiceFS itself prefers, since concurrent reads of the *same* directory
// serialise at the kernel layer.
func buildPathIndex(ctx context.Context, dataPath string) (*pathIndex, error) {
	idx := &pathIndex{
		present: make(map[string]struct{}),
		byType:  make(map[core.FileType]*fileTypeStats),
	}
	if dataPath == "" {
		return idx, nil
	}

	var mu sync.Mutex
	addFile := func(relPath string, ft core.FileType, size int64) {
		mu.Lock()
		defer mu.Unlock()
		idx.present[relPath] = struct{}{}
		idx.totalBytes += size
		stats := idx.byType[ft]
		if stats == nil {
			stats = &fileTypeStats{}
			idx.byType[ft] = stats
		}
		stats.count++
		stats.bytes += size
	}

	walkSubtree := func(rootRel string) {
		_ = filepath.WalkDir(filepath.Join(dataPath, rootRel), func(p string, d fs.DirEntry, werr error) error {
			if werr != nil {
				// Per-entry I/O errors (e.g. transient JuiceFS hiccup) are
				// logged-by-omission: the missing file just won't appear in
				// the index, which downstream code already treats as "absent".
				return nil
			}
			if d.IsDir() {
				if d.Name() == ".git" {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.Contains(p, "/.git/") || store.IsTempFile(d.Name()) {
				return nil
			}
			if cerr := ctx.Err(); cerr != nil {
				return cerr
			}
			info, ierr := d.Info()
			if ierr != nil {
				return nil
			}
			rel, rerr := filepath.Rel(dataPath, p)
			if rerr != nil {
				return nil
			}
			rel = filepath.ToSlash(rel)
			ft := classifyFileType(dataPath, p, d.Name())
			addFile(rel, ft, info.Size())
			return nil
		})
	}

	sem := make(chan struct{}, indexWalkConcurrency)
	var wg sync.WaitGroup

	// fanOut descends levels of directories before launching parallel walks,
	// so the heavy seasons/{year}/... subtree doesn't get processed by a
	// single goroutine. Files encountered above the fan-out depth are stat'd
	// inline by the caller (which is the goroutine running fanOut itself, so
	// keeping that work small matters).
	var fanOut func(rel string, depthLeft int)
	fanOut = func(rel string, depthLeft int) {
		full := dataPath
		if rel != "" {
			full = filepath.Join(dataPath, rel)
		}
		entries, rerr := os.ReadDir(full)
		if rerr != nil {
			return
		}
		for _, e := range entries {
			name := e.Name()
			if name == ".git" {
				continue
			}
			childRel := name
			if rel != "" {
				childRel = filepath.ToSlash(filepath.Join(rel, name))
			}
			if !e.IsDir() {
				if store.IsTempFile(name) {
					continue
				}
				info, ierr := e.Info()
				if ierr != nil {
					continue
				}
				ft := classifyFileType(dataPath, filepath.Join(full, name), name)
				addFile(childRel, ft, info.Size())
				continue
			}
			if depthLeft > 0 {
				fanOut(childRel, depthLeft-1)
				continue
			}
			wg.Add(1)
			sem <- struct{}{}
			go func(r string) {
				defer wg.Done()
				defer func() { <-sem }()
				walkSubtree(r)
			}(childRel)
		}
	}

	if _, err := os.ReadDir(dataPath); err != nil {
		return nil, fmt.Errorf("read data path %q: %w", dataPath, err)
	}
	fanOut("", indexFanoutDepth-1)
	wg.Wait()

	if cerr := ctx.Err(); cerr != nil {
		return nil, cerr
	}
	return idx, nil
}

// indexedStorage delegates Exists to a precomputed pathIndex while passing
// every other Storage method through to the wrapped implementation. Only safe
// to use within the lifetime of the metrics tick that built the index — stale
// presence info would mislead any caller that expects fresh disk state.
type indexedStorage struct {
	inner store.Storage
	idx   *pathIndex
}

func newIndexedStorage(inner store.Storage, idx *pathIndex) *indexedStorage {
	return &indexedStorage{inner: inner, idx: idx}
}

// WithFileType forwards the file type label to the wrapped storage, so reads
// that pass through to it keep their metrics label.
func (s *indexedStorage) WithFileType(ft core.FileType) store.Storage {
	return &indexedStorage{inner: store.WithFileType(s.inner, ft), idx: s.idx}
}

func (s *indexedStorage) Read(ctx context.Context, p string) ([]byte, error) {
	return s.inner.Read(ctx, p)
}

func (s *indexedStorage) Write(ctx context.Context, p string, data []byte) error {
	return s.inner.Write(ctx, p, data)
}

func (s *indexedStorage) Exists(_ context.Context, p string) bool {
	return s.idx.has(p)
}

func (s *indexedStorage) Delete(ctx context.Context, p string) error {
	return s.inner.Delete(ctx, p)
}

func (s *indexedStorage) List(ctx context.Context, dir, ext string) ([]string, error) {
	return s.inner.List(ctx, dir, ext)
}

func (s *indexedStorage) Stat(ctx context.Context, p string) (os.FileInfo, error) {
	return s.inner.Stat(ctx, p)
}
