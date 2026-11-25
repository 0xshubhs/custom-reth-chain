package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
)

// Why this exists
//
// reth keeps the newest canonical blocks in memory and writes them to disk in
// batches: the engine persists once the in-memory tip is MORE than 2 blocks
// past the last persisted block (DEFAULT_PERSISTENCE_THRESHOLD = 2 in
// reth 2.0.0 crates/engine/primitives/src/config.rs, checked in
// crates/engine/tree/src/tree/mod.rs should_persist). On this chain a block is
// sealed only when a transaction arrives, so at rest the newest 0, 1 or 2
// blocks are often in memory only.
//
// reth writes them out on a graceful shutdown, but this node never shuts down
// gracefully: its SIGTERM/SIGINT handler only logs, so kill -9 is the only
// stop, and a kill -9 (or docker pause + copy, or a power cut) loses those
// blocks. `persisted` reads, without touching the node, how far the datadir on
// disk actually reaches, so a stop can be made only when nothing is in memory.
//
// How it reads it: headers live in static files
// (static_files/static_file_headers_<start>_<end>, a NippyJar). Its offsets
// file (.off) is one byte holding the offset width (8) followed by one 8-byte
// offset per column per row plus a final one; the headers segment has 3
// columns (crates/static-file/types/src/segment.rs). So rows = ((size - 1) / 8 - 1) / 3, and
// the highest header on disk is start + rows - 1. reth commits the offsets
// file at the end of every persistence run.

var headersOff = regexp.MustCompile(`^static_file_headers_(\d+)_(\d+)\.off$`)

const (
	nippyOffsetBytes = 8
	headersColumns   = 3
)

type persistedInfo struct {
	Highest int64 // -1 when no header is on disk
	Files   []string
}

// persistedHeight returns the highest block number whose header reth has
// written to the datadir's static files.
func persistedHeight(datadir string) (persistedInfo, error) {
	dir := filepath.Join(datadir, "static_files")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return persistedInfo{}, fmt.Errorf("read %s: %w (is --datadir the node's datadir?)", dir, err)
	}
	type seg struct {
		start uint64
		name  string
	}
	var segs []seg
	for _, e := range entries {
		m := headersOff.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		start, _ := strconv.ParseUint(m[1], 10, 64)
		segs = append(segs, seg{start, e.Name()})
	}
	if len(segs) == 0 {
		return persistedInfo{}, fmt.Errorf("no static_file_headers_*.off in %s", dir)
	}
	sort.Slice(segs, func(i, j int) bool { return segs[i].start < segs[j].start })
	info := persistedInfo{Highest: -1}
	for _, s := range segs {
		st, err := os.Stat(filepath.Join(dir, s.name))
		if err != nil {
			return persistedInfo{}, err
		}
		info.Files = append(info.Files, s.name)
		size := st.Size()
		if size < 1+nippyOffsetBytes {
			continue
		}
		offsets := (size - 1) / nippyOffsetBytes
		if (size-1)%nippyOffsetBytes != 0 || (offsets-1)%headersColumns != 0 {
			return persistedInfo{}, fmt.Errorf("%s: size %d does not fit the headers offsets layout (reth storage format changed?)", s.name, size)
		}
		rows := (offsets - 1) / headersColumns
		if rows > 0 {
			info.Highest = int64(s.start) + rows - 1
		}
	}
	return info, nil
}
