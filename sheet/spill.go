package sheet

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"runtime"
	"time"
)

// Spill codes. Written as the first byte of a block so a reader knows the
// backing without consulting anything else.
const (
	spillFloat  byte = 1
	spillString byte = 2
	spillBool   byte = 3
	spillTime   byte = 4
)

// spillStore holds evicted columns outside the heap.
//
// Restoring a column costs a sequential read; rebuilding one costs a pass over
// every row map, extracting one key and appending. The read is roughly an order
// of magnitude cheaper, which is the only reason this exists — a column can
// always be rebuilt from rows that are still in memory, so spilling has to earn
// its place on speed rather than possibility.
//
// Nothing here is load-bearing. Any failure leaves the column absent, and an
// absent column is rebuilt.
type spillStore struct {
	f      *os.File
	offset int64
	index  map[string]spillEntry
	// cleanupPath is non-empty only where a file cannot be unlinked while
	// open, and names what Close must remove.
	cleanupPath string
}

type spillEntry struct {
	offset int64
	length int64
}

// newSpillStore creates the backing file.
//
// The file is unlinked immediately while the descriptor stays open, so the
// kernel reclaims it when the descriptor closes — on return, on panic, on
// SIGKILL. No code path can orphan it, which is not a property any defer-based
// cleanup has. Windows refuses to unlink an open file, so there the path is
// remembered and removed by Close.
func newSpillStore() (*spillStore, error) {
	f, err := os.CreateTemp("", "dql-sheet-*.spill")
	if err != nil {
		return nil, err
	}
	s := &spillStore{f: f, index: map[string]spillEntry{}}
	if runtime.GOOS == "windows" {
		s.cleanupPath = f.Name()
		return s, nil
	}
	if err := os.Remove(f.Name()); err != nil {
		// Keep going with a named file rather than failing the query; Close
		// removes it.
		s.cleanupPath = f.Name()
	}
	return s, nil
}

func (s *spillStore) Close() error {
	if s == nil || s.f == nil {
		return nil
	}
	err := s.f.Close()
	s.f = nil
	if s.cleanupPath != "" {
		_ = os.Remove(s.cleanupPath)
		s.cleanupPath = ""
	}
	return err
}

// Put writes a column. Reports false when the column has no spillable form,
// which is the AnyColumn case: its elements are arbitrary and encoding them
// would mean owning a serialisation format for values this package never
// inspects. Those are dropped and rebuilt instead.
func (s *spillStore) Put(name string, col Column) bool {
	if s == nil || s.f == nil {
		return false
	}
	buf, ok := encodeColumn(col)
	if !ok {
		return false
	}
	n, err := s.f.WriteAt(buf, s.offset)
	if err != nil || n != len(buf) {
		return false
	}
	s.index[name] = spillEntry{offset: s.offset, length: int64(len(buf))}
	s.offset += int64(len(buf))
	return true
}

// Get restores a column. Reports false when it was never spilled or cannot be
// read back, and the caller rebuilds.
func (s *spillStore) Get(name string) (Column, bool) {
	if s == nil || s.f == nil {
		return nil, false
	}
	e, ok := s.index[name]
	if !ok {
		return nil, false
	}
	buf := make([]byte, e.length)
	if _, err := s.f.ReadAt(buf, e.offset); err != nil && err != io.EOF {
		return nil, false
	}
	return decodeColumn(buf)
}

// --- encoding ---
//
// Length-prefixed typed blocks. A spilled column is always read back whole and
// sequentially, so there is no index inside a block and no need for one.

func encodeColumn(col Column) ([]byte, bool) {
	n := col.Len()
	switch c := col.(type) {
	case *FloatColumn:
		buf := newBlock(spillFloat, n, 8*n)
		for i, v := range c.Floats() {
			binary.LittleEndian.PutUint64(buf[blockHeader+8*i:], math.Float64bits(v))
		}
		return appendNulls(buf, col, n), true

	case *BoolColumn:
		buf := newBlock(spillBool, n, n)
		for i := 0; i < n; i++ {
			if v, _ := c.At(i).(bool); v {
				buf[blockHeader+i] = 1
			}
		}
		return appendNulls(buf, col, n), true

	case *TimeColumn:
		buf := newBlock(spillTime, n, 8*n)
		for i, v := range c.Times() {
			binary.LittleEndian.PutUint64(buf[blockHeader+8*i:], uint64(v.UnixNano()))
		}
		return appendNulls(buf, col, n), true

	case *StringColumn:
		vals := c.Strings()
		size := 4 * n
		for _, v := range vals {
			size += len(v)
		}
		buf := newBlock(spillString, n, size)
		off := blockHeader
		for _, v := range vals {
			binary.LittleEndian.PutUint32(buf[off:], uint32(len(v)))
			off += 4
			copy(buf[off:], v)
			off += len(v)
		}
		return appendNulls(buf, col, n), true
	}
	// AnyColumn and anything a host adds later: not spillable, rebuild instead.
	return nil, false
}

// blockHeader is one type byte plus a four-byte length.
const blockHeader = 5

func newBlock(kind byte, n, payload int) []byte {
	buf := make([]byte, blockHeader+payload, blockHeader+payload+(n+7)/8)
	buf[0] = kind
	binary.LittleEndian.PutUint32(buf[1:], uint32(n))
	return buf
}

func appendNulls(buf []byte, col Column, n int) []byte {
	nulls := make([]byte, (n+7)/8)
	for i := 0; i < n; i++ {
		if col.IsNull(i) {
			nulls[i/8] |= 1 << uint(i%8)
		}
	}
	return append(buf, nulls...)
}

func decodeColumn(buf []byte) (Column, bool) {
	if len(buf) < blockHeader {
		return nil, false
	}
	kind := buf[0]
	n := int(binary.LittleEndian.Uint32(buf[1:]))
	if n < 0 {
		return nil, false
	}
	body := buf[blockHeader:]

	payload := 0
	switch kind {
	case spillFloat, spillTime:
		payload = 8 * n
	case spillBool:
		payload = n
	case spillString:
		// Variable; located by walking below.
	default:
		return nil, false
	}

	var col Column
	switch kind {
	case spillFloat:
		if len(body) < payload {
			return nil, false
		}
		vals := make([]float64, n)
		for i := range vals {
			vals[i] = math.Float64frombits(binary.LittleEndian.Uint64(body[8*i:]))
		}
		col = NewFloatColumn(vals, nil)

	case spillTime:
		if len(body) < payload {
			return nil, false
		}
		vals := make([]time.Time, n)
		for i := range vals {
			vals[i] = time.Unix(0, int64(binary.LittleEndian.Uint64(body[8*i:]))).UTC()
		}
		col = NewTimeColumn(vals, nil)

	case spillBool:
		if len(body) < payload {
			return nil, false
		}
		vals := make([]bool, n)
		for i := range vals {
			vals[i] = body[i] == 1
		}
		col = NewBoolColumn(vals, nil)

	case spillString:
		vals := make([]string, n)
		off := 0
		for i := 0; i < n; i++ {
			if off+4 > len(body) {
				return nil, false
			}
			ln := int(binary.LittleEndian.Uint32(body[off:]))
			off += 4
			if off+ln > len(body) {
				return nil, false
			}
			vals[i] = string(body[off : off+ln])
			off += ln
		}
		payload = off
		col = NewStringColumn(vals, nil)
	}

	nulls, ok := decodeNulls(body[payload:], n)
	if !ok {
		return nil, false
	}
	return withNulls(col, nulls), true
}

func decodeNulls(b []byte, n int) (*Bitmap, bool) {
	want := (n + 7) / 8
	if len(b) < want {
		return nil, false
	}
	bm := NewBitmap(n)
	for i := 0; i < n; i++ {
		if b[i/8]&(1<<uint(i%8)) != 0 {
			bm.Set(i)
		}
	}
	return bm, true
}

// withNulls reattaches a decoded bitmap. The constructors take one at build
// time and decoding learns the values first, so this rebinds rather than
// threading the bitmap through every branch above.
func withNulls(col Column, nulls *Bitmap) Column {
	switch c := col.(type) {
	case *FloatColumn:
		return NewFloatColumn(c.Floats(), nulls)
	case *StringColumn:
		return NewStringColumn(c.Strings(), nulls)
	case *TimeColumn:
		return NewTimeColumn(c.Times(), nulls)
	case *BoolColumn:
		vals := make([]bool, c.Len())
		for i := range vals {
			vals[i], _ = c.At(i).(bool)
		}
		return NewBoolColumn(vals, nulls)
	}
	return col
}

// columnBytes estimates a column's heap cost, for deciding what to evict.
// Approximate on purpose: exactness would cost more than the decision is worth.
func columnBytes(col Column) int {
	n := col.Len()
	switch c := col.(type) {
	case *FloatColumn, *TimeColumn:
		return 8*n + (n+7)/8
	case *BoolColumn:
		return n + (n+7)/8
	case *StringColumn:
		total := 16 * n
		for _, v := range c.Strings() {
			total += len(v)
		}
		return total + (n+7)/8
	case *AnyColumn:
		// An interface word pair per slot, plus whatever each points at, which
		// is not knowable from here. The pointee is charged as one word.
		return 24 * n
	}
	return 8 * n
}

func (s *spillStore) String() string {
	if s == nil {
		return "spill(disabled)"
	}
	return fmt.Sprintf("spill(%d columns, %d bytes)", len(s.index), s.offset)
}
