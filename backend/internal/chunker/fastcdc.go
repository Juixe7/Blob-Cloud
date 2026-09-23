package chunker

import (
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/bits"
)

// Default chunk size constants for high-throughput cloud storage CAS blocks.
const (
	DefaultMinSize = 1 * 1024 * 1024 // 1 MiB (2^20 bytes)
	DefaultAvgSize = 4 * 1024 * 1024 // 4 MiB (2^22 bytes)
	DefaultMaxSize = 8 * 1024 * 1024 // 8 MiB (2^23 bytes)
)

// Chunk represents an immutable block cut by the FastCDC engine.
type Chunk struct {
	// Offset is the zero-based byte offset of the chunk start in the source stream.
	Offset int64 `json:"offset"`
	// Length is the exact size of the chunk in bytes.
	Length int `json:"length"`
	// SHA256 is the hexadecimal lowercase SHA-256 digest of the chunk's payload.
	SHA256 string `json:"sha256"`
	// BlockMD5 is the hexadecimal lowercase MD5 digest (useful for S3 Content-MD5 / ETag).
	BlockMD5 string `json:"block_md5"`
	// Data holds the raw chunk bytes if Config.RetainData is enabled.
	Data []byte `json:"-"`
}

// Config specifies the mathematical parameters and boundaries for FastCDC.
type Config struct {
	// MinSize is the strict floor for chunk size (except the terminal EOF chunk).
	MinSize int
	// AvgSize is the target expected chunk size.
	AvgSize int
	// MaxSize is the strict ceiling for chunk size.
	MaxSize int
	// Normalization delta (default: 1 bit).
	NormDelta int
	// MaskSmall is the stricter mask applied in [MinSize, AvgSize).
	MaskSmall uint64
	// MaskLarge is the looser mask applied in [AvgSize, MaxSize).
	MaskLarge uint64
	// RetainData indicates whether Chunk.Data should be populated.
	RetainData bool
}

// DefaultConfig returns production-ready FastCDC configuration (1MB / 4MB / 8MB).
func DefaultConfig() Config {
	cfg := Config{
		MinSize:    DefaultMinSize,
		AvgSize:    DefaultAvgSize,
		MaxSize:    DefaultMaxSize,
		NormDelta:  1,
		RetainData: false,
	}
	cfg.initMasks()
	return cfg
}

// initMasks computes the FastCDC normalized dual masks if not explicitly configured.
func (c *Config) initMasks() {
	if c.AvgSize <= 0 {
		c.AvgSize = DefaultAvgSize
	}
	if c.MinSize <= 0 {
		c.MinSize = c.AvgSize / 4
		if c.MinSize < 64 {
			c.MinSize = 64
		}
	}
	if c.MaxSize <= 0 {
		c.MaxSize = c.AvgSize * 2
	}
	if c.NormDelta <= 0 {
		c.NormDelta = 1
	}

	// Calculate base bits k = floor(log2(AvgSize))
	k := bits.Len(uint(c.AvgSize)) - 1
	if k < 4 {
		k = 4
	}

	if c.MaskSmall == 0 {
		smallBits := k + c.NormDelta
		if smallBits > 62 {
			smallBits = 62
		}
		c.MaskSmall = (uint64(1) << smallBits) - 1
	}

	if c.MaskLarge == 0 {
		largeBits := k - c.NormDelta
		if largeBits < 1 {
			largeBits = 1
		}
		c.MaskLarge = (uint64(1) << largeBits) - 1
	}
}

// Validate ensures boundaries satisfy mathematical invariants:
// 0 < MinSize <= AvgSize <= MaxSize.
func (c *Config) Validate() error {
	c.initMasks()
	if c.MinSize <= 0 {
		return errors.New("fastcdc: MinSize must be > 0")
	}
	if c.AvgSize < c.MinSize {
		return fmt.Errorf("fastcdc: AvgSize (%d) cannot be less than MinSize (%d)", c.AvgSize, c.MinSize)
	}
	if c.MaxSize < c.AvgSize {
		return fmt.Errorf("fastcdc: MaxSize (%d) cannot be less than AvgSize (%d)", c.MaxSize, c.AvgSize)
	}
	return nil
}

// Chunker is a streaming FastCDC reader that partitions an arbitrary io.Reader
// into content-defined chunks with minimal memory footprint and zero-allocation sliding loops.
type Chunker struct {
	r      io.Reader
	cfg    Config
	buf    []byte
	start  int   // offset in buf where unconsumed data begins
	end    int   // offset in buf where valid data ends
	offset int64 // cumulative stream offset
	eof    bool
	err    error
}

// NewChunker creates a new streaming FastCDC chunker reading from r.
func NewChunker(r io.Reader, cfg Config) (*Chunker, error) {
	if r == nil {
		return nil, errors.New("fastcdc: reader cannot be nil")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	// Buffer holds at least 2 * MaxSize so we always have contiguous window space
	// for full chunk scanning without excessive memory copying.
	bufCap := cfg.MaxSize * 2
	if bufCap < 16*1024*1024 {
		bufCap = 16 * 1024 * 1024
	}

	return &Chunker{
		r:   r,
		cfg: cfg,
		buf: make([]byte, bufCap),
	}, nil
}

// Next cuts and returns the next content-defined Chunk from the stream.
// Returns (nil, io.EOF) when all data has been processed.
func (c *Chunker) Next() (*Chunk, error) {
	if c.err != nil {
		return nil, c.err
	}

	for {
		available := c.end - c.start

		// If we haven't reached EOF and have less than MaxSize available, fill the buffer.
		if !c.eof && available < c.cfg.MaxSize {
			c.shiftAndFill()
			available = c.end - c.start
		}

		// If buffer is completely drained and EOF reached, we are done.
		if available == 0 {
			if c.eof {
				return nil, io.EOF
			}
			continue
		}

		// If remaining data is smaller than MinSize and EOF reached, emit terminal chunk.
		if available <= c.cfg.MinSize {
			if c.eof {
				chunk := c.emitChunk(available)
				return chunk, nil
			}
			// Otherwise need more data to meet MinSize invariant
			c.shiftAndFill()
			continue
		}

		// Find cut boundary using FastCDC rolling Gear hash.
		chunkLen := c.findCut(available)
		chunk := c.emitChunk(chunkLen)
		return chunk, nil
	}
}

// shiftAndFill moves remaining unconsumed bytes to the buffer head and reads more from r.
func (c *Chunker) shiftAndFill() {
	if c.start > 0 {
		copy(c.buf, c.buf[c.start:c.end])
		c.end -= c.start
		c.start = 0
	}

	for c.end < len(c.buf) && !c.eof {
		n, err := c.r.Read(c.buf[c.end:])
		if n > 0 {
			c.end += n
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				c.eof = true
			} else {
				c.err = err
			}
			return
		}
		// If we have accumulated at least MaxSize, that's enough to find the next cut.
		if c.end-c.start >= c.cfg.MaxSize {
			return
		}
	}
}

// findCut executes the FastCDC normalized rolling hash over the buffer slice.
func (c *Chunker) findCut(available int) int {
	maxCut := available
	if maxCut > c.cfg.MaxSize {
		maxCut = c.cfg.MaxSize
	}

	// Optimization: Skip the first MinSize bytes without hashing.
	// This yields a ~25% CPU reduction on average 4MB chunks.
	var hash uint64
	data := c.buf[c.start : c.start+maxCut]

	// Phase 1: Sub-Average Region [MinSize, AvgSize) -> use MaskSmall (stricter mask)
	midPoint := c.cfg.AvgSize
	if midPoint > maxCut {
		midPoint = maxCut
	}

	for i := c.cfg.MinSize; i < midPoint; i++ {
		hash = (hash << 1) + GearTable[data[i]]
		if (hash & c.cfg.MaskSmall) == 0 {
			return i + 1
		}
	}

	// Phase 2: Post-Average Region [AvgSize, MaxSize) -> use MaskLarge (looser mask)
	for i := midPoint; i < maxCut; i++ {
		hash = (hash << 1) + GearTable[data[i]]
		if (hash & c.cfg.MaskLarge) == 0 {
			return i + 1
		}
	}

	// Phase 3: Hard Ceiling reached at MaxSize (or available EOF)
	return maxCut
}

// emitChunk constructs the Chunk struct, computes cryptographic hashes, and updates offsets.
func (c *Chunker) emitChunk(chunkLen int) *Chunk {
	slice := c.buf[c.start : c.start+chunkLen]

	// Compute SHA-256
	shaDigest := sha256.Sum256(slice)
	shaHex := hex.EncodeToString(shaDigest[:])

	// Compute MD5 for S3 ETag verification
	md5Digest := md5.Sum(slice)
	md5Hex := hex.EncodeToString(md5Digest[:])

	chunk := &Chunk{
		Offset:   c.offset,
		Length:   chunkLen,
		SHA256:   shaHex,
		BlockMD5: md5Hex,
	}

	if c.cfg.RetainData {
		chunk.Data = make([]byte, chunkLen)
		copy(chunk.Data, slice)
	}

	c.start += chunkLen
	c.offset += int64(chunkLen)
	return chunk
}

// Split is a zero-copy convenience function that splits an in-memory byte slice
// into FastCDC chunks using the supplied configuration.
func Split(data []byte, cfg Config) ([]Chunk, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	n := len(data)
	if n == 0 {
		return nil, nil
	}

	var chunks []Chunk
	var offset int64

	for offset < int64(n) {
		remaining := n - int(offset)
		if remaining <= cfg.MinSize {
			// Terminal chunk
			chunkBytes := data[offset:]
			sha := sha256.Sum256(chunkBytes)
			m5 := md5.Sum(chunkBytes)
			c := Chunk{
				Offset:   offset,
				Length:   remaining,
				SHA256:   hex.EncodeToString(sha[:]),
				BlockMD5: hex.EncodeToString(m5[:]),
			}
			if cfg.RetainData {
				c.Data = chunkBytes
			}
			chunks = append(chunks, c)
			break
		}

		maxCut := remaining
		if maxCut > cfg.MaxSize {
			maxCut = cfg.MaxSize
		}

		slice := data[offset : offset+int64(maxCut)]
		var hash uint64
		cut := maxCut

		midPoint := cfg.AvgSize
		if midPoint > maxCut {
			midPoint = maxCut
		}

		found := false
		for i := cfg.MinSize; i < midPoint; i++ {
			hash = (hash << 1) + GearTable[slice[i]]
			if (hash & cfg.MaskSmall) == 0 {
				cut = i + 1
				found = true
				break
			}
		}

		if !found {
			for i := midPoint; i < maxCut; i++ {
				hash = (hash << 1) + GearTable[slice[i]]
				if (hash & cfg.MaskLarge) == 0 {
					cut = i + 1
					break
				}
			}
		}

		chunkBytes := data[offset : offset+int64(cut)]
		sha := sha256.Sum256(chunkBytes)
		m5 := md5.Sum(chunkBytes)
		c := Chunk{
			Offset:   offset,
			Length:   cut,
			SHA256:   hex.EncodeToString(sha[:]),
			BlockMD5: hex.EncodeToString(m5[:]),
		}
		if cfg.RetainData {
			c.Data = chunkBytes
		}
		chunks = append(chunks, c)
		offset += int64(cut)
	}

	return chunks, nil
}
