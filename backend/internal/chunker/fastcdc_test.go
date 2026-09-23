package chunker

import (
	"bytes"
	"crypto/rand"
	"io"
	"testing"
)

func TestFastCDC_EmptyAndSmallFiles(t *testing.T) {
	cfg := DefaultConfig()

	// 0-byte input
	chunks, err := Split([]byte{}, cfg)
	if err != nil {
		t.Fatalf("Split empty: %v", err)
	}
	if len(chunks) != 0 {
		t.Fatalf("expected 0 chunks, got %d", len(chunks))
	}

	// Small input (< MinSize)
	smallData := []byte("Hello, Blob-Cloud FastCDC!")
	chunks, err = Split(smallData, cfg)
	if err != nil {
		t.Fatalf("Split small: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk for small file, got %d", len(chunks))
	}
	if chunks[0].Length != len(smallData) {
		t.Fatalf("expected chunk length %d, got %d", len(smallData), chunks[0].Length)
	}
	if chunks[0].Offset != 0 {
		t.Fatalf("expected offset 0, got %d", chunks[0].Offset)
	}
}

func TestFastCDC_StreamingEquivalence(t *testing.T) {
	// Generate 4MB test payload
	data := make([]byte, 4*1024*1024)
	if _, err := rand.Read(data); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}

	cfg := Config{
		MinSize: 16 * 1024,
		AvgSize: 64 * 1024,
		MaxSize: 128 * 1024,
	}

	// 1. Split in-memory
	splitChunks, err := Split(data, cfg)
	if err != nil {
		t.Fatalf("Split: %v", err)
	}

	// 2. Chunker streaming
	chunker, err := NewChunker(bytes.NewReader(data), cfg)
	if err != nil {
		t.Fatalf("NewChunker: %v", err)
	}

	var streamChunks []Chunk
	for {
		c, err := chunker.Next()
		if err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("chunker.Next: %v", err)
		}
		streamChunks = append(streamChunks, *c)
	}

	if len(splitChunks) != len(streamChunks) {
		t.Fatalf("chunk count mismatch: Split got %d, Chunker got %d", len(splitChunks), len(streamChunks))
	}

	for i := range splitChunks {
		sc := splitChunks[i]
		stc := streamChunks[i]
		if sc.Offset != stc.Offset {
			t.Fatalf("chunk %d offset mismatch: %d vs %d", i, sc.Offset, stc.Offset)
		}
		if sc.Length != stc.Length {
			t.Fatalf("chunk %d length mismatch: %d vs %d", i, sc.Length, stc.Length)
		}
		if sc.SHA256 != stc.SHA256 {
			t.Fatalf("chunk %d SHA-256 mismatch: %s vs %s", i, sc.SHA256, stc.SHA256)
		}
	}
}

func TestFastCDC_BoundaryGuarantees(t *testing.T) {
	cfg := Config{
		MinSize: 32 * 1024,
		AvgSize: 128 * 1024,
		MaxSize: 256 * 1024,
	}

	// Test 1: Random data
	randomData := make([]byte, 2*1024*1024)
	if _, err := rand.Read(randomData); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}

	// Test 2: Low-entropy zero bytes
	zeroData := make([]byte, 2*1024*1024)

	// Test 3: Low-entropy 0xFF bytes
	ffData := bytes.Repeat([]byte{0xFF}, 2*1024*1024)

	testCases := []struct {
		name string
		data []byte
	}{
		{"RandomData", randomData},
		{"AllZeroes", zeroData},
		{"AllFFs", ffData},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			chunks, err := Split(tc.data, cfg)
			if err != nil {
				t.Fatalf("Split error: %v", err)
			}

			var totalBytes int64
			for i, c := range chunks {
				isTerminal := (i == len(chunks)-1)

				if !isTerminal && c.Length < cfg.MinSize {
					t.Fatalf("chunk %d length %d is below MinSize %d", i, c.Length, cfg.MinSize)
				}
				if c.Length > cfg.MaxSize {
					t.Fatalf("chunk %d length %d exceeds MaxSize %d", i, c.Length, cfg.MaxSize)
				}
				if c.Offset != totalBytes {
					t.Fatalf("chunk %d offset expected %d, got %d", i, totalBytes, c.Offset)
				}
				totalBytes += int64(c.Length)
			}

			if totalBytes != int64(len(tc.data)) {
				t.Fatalf("total chunked bytes %d != data length %d", totalBytes, len(tc.data))
			}
		})
	}
}

func TestFastCDC_ShiftResistance(t *testing.T) {
	// Base data: 5 MiB of pseudorandom data
	baseSize := 5 * 1024 * 1024
	baseData := make([]byte, baseSize)
	if _, err := rand.Read(baseData); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}

	cfg := Config{
		MinSize: 16 * 1024,
		AvgSize: 64 * 1024,
		MaxSize: 128 * 1024,
	}

	// Chunk base file
	baseChunks, err := Split(baseData, cfg)
	if err != nil {
		t.Fatalf("Split base: %v", err)
	}
	if len(baseChunks) < 10 {
		t.Fatalf("expected at least 10 chunks, got %d", len(baseChunks))
	}

	baseHashSet := make(map[string]bool, len(baseChunks))
	for _, c := range baseChunks {
		baseHashSet[c.SHA256] = true
	}

	// 1. Shift by 1 byte (Prepend single byte)
	shifted1 := append([]byte{0x42}, baseData...)
	chunks1, err := Split(shifted1, cfg)
	if err != nil {
		t.Fatalf("Split shifted1: %v", err)
	}

	sharedCount1 := 0
	for _, c := range chunks1 {
		if baseHashSet[c.SHA256] {
			sharedCount1++
		}
	}

	// Calculate deduplication overlap ratio
	overlapRatio1 := float64(sharedCount1) / float64(len(baseChunks))
	t.Logf("Shift by 1 byte: %d / %d chunks matched (%.2f%% deduplication)", sharedCount1, len(baseChunks), overlapRatio1*100)

	if overlapRatio1 < 0.85 {
		t.Fatalf("expected >= 85%% deduplication on 1-byte shift, got %.2f%%", overlapRatio1*100)
	}

	// Compare with Fixed-Size Chunking on the exact same 1-byte shift
	fixedChunkSize := 64 * 1024
	fixedBaseHashes := make(map[int]string)
	for i := 0; i*fixedChunkSize < baseSize; i++ {
		end := (i + 1) * fixedChunkSize
		if end > baseSize {
			end = baseSize
		}
		c := baseData[i*fixedChunkSize : end]
		fixedBaseHashes[i] = string(c)
	}

	fixedMatched := 0
	for i := 0; i*fixedChunkSize < len(shifted1); i++ {
		end := (i + 1) * fixedChunkSize
		if end > len(shifted1) {
			end = len(shifted1)
		}
		c := string(shifted1[i*fixedChunkSize : end])
		for _, bh := range fixedBaseHashes {
			if bh == c {
				fixedMatched++
				break
			}
		}
	}
	t.Logf("Fixed 64KB chunking on 1-byte shift matched: %d / %d chunks (Catastrophic collapse)", fixedMatched, len(fixedBaseHashes))
	if fixedMatched > 1 {
		t.Fatalf("Fixed chunking should have collapsed, but matched %d", fixedMatched)
	}

	// 2. Shift by 17 bytes (Prepend 17 bytes)
	shifted17 := append(bytes.Repeat([]byte{0x7F}, 17), baseData...)
	chunks17, err := Split(shifted17, cfg)
	if err != nil {
		t.Fatalf("Split shifted17: %v", err)
	}

	sharedCount17 := 0
	for _, c := range chunks17 {
		if baseHashSet[c.SHA256] {
			sharedCount17++
		}
	}
	overlapRatio17 := float64(sharedCount17) / float64(len(baseChunks))
	t.Logf("Shift by 17 bytes: %d / %d chunks matched (%.2f%% deduplication)", sharedCount17, len(baseChunks), overlapRatio17*100)
	if overlapRatio17 < 0.85 {
		t.Fatalf("expected >= 85%% deduplication on 17-byte shift, got %.2f%%", overlapRatio17*100)
	}
}

func BenchmarkFastCDC_Split_10MB(b *testing.B) {
	data := make([]byte, 10*1024*1024)
	if _, err := rand.Read(data); err != nil {
		b.Fatalf("rand.Read: %v", err)
	}
	cfg := DefaultConfig()

	b.SetBytes(int64(len(data)))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := Split(data, cfg)
		if err != nil {
			b.Fatalf("Split: %v", err)
		}
	}
}

func BenchmarkFastCDC_Streaming_10MB(b *testing.B) {
	data := make([]byte, 10*1024*1024)
	if _, err := rand.Read(data); err != nil {
		b.Fatalf("rand.Read: %v", err)
	}
	cfg := DefaultConfig()

	b.SetBytes(int64(len(data)))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		r := bytes.NewReader(data)
		chunker, err := NewChunker(r, cfg)
		if err != nil {
			b.Fatalf("NewChunker: %v", err)
		}
		for {
			_, err := chunker.Next()
			if err != nil {
				if err == io.EOF {
					break
				}
				b.Fatalf("Next: %v", err)
			}
		}
	}
}
