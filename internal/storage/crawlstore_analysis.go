package storage

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"

	"github.com/micelio/micelio/internal/types"
)

// Post-crawl analysis results stored next to the pages so a reload does not
// rebuild them: the link graph in CSR form (decoding every page blob a
// second time just to read its links) and per-page PageRank / HITS scores
// (PageRank no longer lives in the stats blob; HITS used to be recomputed on
// every reload).
//
// Arrays are stored in chunks of analysisChunkLen values, each zstd
// compressed, in table analysis_arrays. A JSON header in crawl_meta
// (graphMetaKey / scoresMetaKey) records the format version, sizes and a
// hash of the page URL order: node i is the i-th page by rowid, and the
// arrays are only used when the reloaded pages hash the same.

const (
	analysisChunkLen  = 1 << 19 // values per chunk (2-4 MB raw; under the 10 MB decoder cap)
	graphMetaKey      = "analysis_graph_v1"
	scoresMetaKey     = "analysis_scores_v1"
	analysisFormatVer = 1
)

const analysisSchema = `
	CREATE TABLE IF NOT EXISTS analysis_arrays (
		name TEXT NOT NULL,
		seq INTEGER NOT NULL,
		data BLOB NOT NULL,
		PRIMARY KEY (name, seq)
	) WITHOUT ROWID;
`

type analysisMeta struct {
	Version  int    `json:"version"`
	N        int    `json:"n"`
	Edges    int    `json:"edges,omitempty"`
	URLsHash uint64 `json:"urlsHash"`
	HasHits  bool   `json:"hasHits,omitempty"`
}

// PageOrderHash fingerprints the URL order of pages (node i = pages[i]).
func PageOrderHash(pages []*types.PageData) uint64 {
	h := fnv.New64a()
	for _, p := range pages {
		h.Write([]byte(p.URL))
		h.Write([]byte{0})
	}
	return h.Sum64()
}

// SaveLinkGraph stores the CSR link graph (offsets has n+1 entries).
func (s *CrawlStore) SaveLinkGraph(urlsHash uint64, offsets, edges []int32) (err error) {
	if len(offsets) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	defer sqlitex.Save(s.conn)(&err)
	if err = s.putInt32s("graph_offsets", offsets); err != nil {
		return err
	}
	if err = s.putInt32s("graph_edges", edges); err != nil {
		return err
	}
	meta, _ := json.Marshal(analysisMeta{Version: analysisFormatVer, N: len(offsets) - 1, Edges: len(edges), URLsHash: urlsHash})
	return s.saveMeta(graphMetaKey, string(meta))
}

// LoadLinkGraph returns the stored CSR graph when it matches urlsHash and n.
func (s *CrawlStore) LoadLinkGraph(urlsHash uint64, n int) (offsets, edges []int32, ok bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	meta, ok, err := s.analysisMeta(graphMetaKey, urlsHash, n)
	if !ok || err != nil {
		return nil, nil, false, err
	}
	if offsets, err = s.getInt32s("graph_offsets", n+1); err != nil {
		return nil, nil, false, err
	}
	if edges, err = s.getInt32s("graph_edges", meta.Edges); err != nil {
		return nil, nil, false, err
	}
	if len(offsets) != n+1 || int(offsets[n]) != len(edges) {
		return nil, nil, false, nil
	}
	return offsets, edges, true, nil
}

// SavePageScores stores per-node PageRank and, when hasHits, HITS
// authority/hub scores.
func (s *CrawlStore) SavePageScores(urlsHash uint64, pageRank, authority, hub []float64, hasHits bool) (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer sqlitex.Save(s.conn)(&err)
	if err = s.putFloat64s("score_pagerank", pageRank); err != nil {
		return err
	}
	if hasHits {
		if err = s.putFloat64s("score_authority", authority); err != nil {
			return err
		}
		if err = s.putFloat64s("score_hub", hub); err != nil {
			return err
		}
	}
	meta, _ := json.Marshal(analysisMeta{Version: analysisFormatVer, N: len(pageRank), URLsHash: urlsHash, HasHits: hasHits})
	return s.saveMeta(scoresMetaKey, string(meta))
}

// LoadPageScores returns stored scores when they match urlsHash and n.
// authority and hub are nil when HITS was not stored.
func (s *CrawlStore) LoadPageScores(urlsHash uint64, n int) (pageRank, authority, hub []float64, ok bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	meta, ok, err := s.analysisMeta(scoresMetaKey, urlsHash, n)
	if !ok || err != nil {
		return nil, nil, nil, false, err
	}
	if pageRank, err = s.getFloat64s("score_pagerank", n); err != nil {
		return nil, nil, nil, false, err
	}
	if meta.HasHits {
		if authority, err = s.getFloat64s("score_authority", n); err != nil {
			return nil, nil, nil, false, err
		}
		if hub, err = s.getFloat64s("score_hub", n); err != nil {
			return nil, nil, nil, false, err
		}
	}
	return pageRank, authority, hub, true, nil
}

func (s *CrawlStore) analysisMeta(key string, urlsHash uint64, n int) (analysisMeta, bool, error) {
	raw, err := s.getMeta(key)
	if err != nil || raw == "" {
		return analysisMeta{}, false, err
	}
	var m analysisMeta
	if json.Unmarshal([]byte(raw), &m) != nil {
		return analysisMeta{}, false, nil
	}
	if m.Version != analysisFormatVer || m.N != n || m.URLsHash != urlsHash {
		return analysisMeta{}, false, nil
	}
	return m, true, nil
}

// putInt32s replaces array name with vals (caller holds s.mu, in a savepoint).
func (s *CrawlStore) putInt32s(name string, vals []int32) error {
	return s.putChunks(name, len(vals), 4, func(buf []byte, i int) {
		binary.LittleEndian.PutUint32(buf, uint32(vals[i]))
	})
}

func (s *CrawlStore) putFloat64s(name string, vals []float64) error {
	return s.putChunks(name, len(vals), 8, func(buf []byte, i int) {
		binary.LittleEndian.PutUint64(buf, math.Float64bits(vals[i]))
	})
}

func (s *CrawlStore) putChunks(name string, n, width int, put func(buf []byte, i int)) error {
	if err := sqlitex.Execute(s.conn, `DELETE FROM analysis_arrays WHERE name = ?`,
		&sqlitex.ExecOptions{Args: []any{name}}); err != nil {
		return err
	}
	raw := make([]byte, min(n, analysisChunkLen)*width)
	for seq, start := 0, 0; start < n; seq, start = seq+1, start+analysisChunkLen {
		end := min(start+analysisChunkLen, n)
		buf := raw[:(end-start)*width]
		for i := start; i < end; i++ {
			put(buf[(i-start)*width:], i)
		}
		blob, err := zstdCompressBlob(buf)
		if err != nil {
			return err
		}
		if err := sqlitex.Execute(s.conn, `INSERT INTO analysis_arrays (name, seq, data) VALUES (?, ?, ?)`,
			&sqlitex.ExecOptions{Args: []any{name, seq, blob}}); err != nil {
			return err
		}
	}
	return nil
}

func (s *CrawlStore) getInt32s(name string, n int) ([]int32, error) {
	out := make([]int32, 0, n)
	err := s.getChunks(name, 4, func(b []byte) {
		out = append(out, int32(binary.LittleEndian.Uint32(b)))
	})
	if err == nil && len(out) != n {
		err = fmt.Errorf("analysis array %s: %d values, want %d", name, len(out), n)
	}
	return out, err
}

func (s *CrawlStore) getFloat64s(name string, n int) ([]float64, error) {
	out := make([]float64, 0, n)
	err := s.getChunks(name, 8, func(b []byte) {
		out = append(out, math.Float64frombits(binary.LittleEndian.Uint64(b)))
	})
	if err == nil && len(out) != n {
		err = fmt.Errorf("analysis array %s: %d values, want %d", name, len(out), n)
	}
	return out, err
}

func (s *CrawlStore) getChunks(name string, width int, each func(b []byte)) error {
	return sqlitex.Execute(s.conn, `SELECT data FROM analysis_arrays WHERE name = ? ORDER BY seq`,
		&sqlitex.ExecOptions{
			Args: []any{name},
			ResultFunc: func(stmt *sqlite.Stmt) error {
				blob := make([]byte, stmt.ColumnLen(0))
				stmt.ColumnBytes(0, blob)
				raw, err := decompressBlob(blob)
				if err != nil {
					return fmt.Errorf("decompress %s: %w", name, err)
				}
				if len(raw)%width != 0 {
					return fmt.Errorf("analysis array %s: bad chunk length %d", name, len(raw))
				}
				for i := 0; i < len(raw); i += width {
					each(raw[i : i+width])
				}
				return nil
			},
		})
}
