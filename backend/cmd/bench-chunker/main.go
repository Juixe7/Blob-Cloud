package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"math/rand"

	"go-drive-clone/internal/chunker"
)

// Simple chunk representation for testing deduplication
type block struct {
	hash string
	size int
}

// splitFixed slices data into fixed 4 MB blocks (standard S3 multipart style)
func splitFixed(data []byte, blockSize int) []block {
	var out []block
	for i := 0; i < len(data); i += blockSize {
		end := i + blockSize
		if end > len(data) {
			end = len(data)
		}
		sum := sha256.Sum256(data[i:end])
		out = append(out, block{hash: hex.EncodeToString(sum[:]), size: end - i})
	}
	return out
}

// splitFastCDC slices data using our production FastCDC config (1 MB min, 4 MB avg, 8 MB max)
func splitFastCDC(data []byte) []block {
	c, err := chunker.NewChunker(bytes.NewReader(data), chunker.DefaultConfig())
	if err != nil {
		panic(err)
	}
	var out []block
	for {
		ch, err := c.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			panic(err)
		}
		out = append(out, block{hash: ch.SHA256, size: ch.Length})
	}
	return out
}

// measureReuse checks how many bytes of the edited file already exist in the CAS map
func measureReuse(base, edited []block) (reusedChunks, totalChunks int, pctSaved float64, uploadedMB float64) {
	cas := make(map[string]bool, len(base))
	for _, b := range base {
		cas[b.hash] = true
	}

	var savedBytes, totalBytes int
	for _, b := range edited {
		totalBytes += b.size
		if cas[b.hash] {
			reusedChunks++
			savedBytes += b.size
		}
	}
	pctSaved = float64(savedBytes) / float64(totalBytes) * 100
	uploadedMB = float64(totalBytes-savedBytes) / (1024 * 1024)
	return reusedChunks, len(edited), pctSaved, uploadedMB
}

func main() {
	const mb = 1024 * 1024
	rng := rand.New(rand.NewSource(42))

	// Create a 100 MB base file
	baseFile := make([]byte, 100*mb)
	rng.Read(baseFile)

	insertData := make([]byte, 64*1024) // 64 KB edit
	rng.Read(insertData)

	baseFixed := splitFixed(baseFile, 4*mb)
	baseCDC := splitFastCDC(baseFile)

	cases := []struct {
		name string
		data []byte
	}{
		{
			name: "1. Prepend 64 KB at start of 100 MB file",
			data: append(append([]byte{}, insertData...), baseFile...),
		},
		{
			name: "2. Insert 64 KB in middle (at 50 MB) of 100 MB file",
			data: append(append([]byte{}, baseFile[:50*mb]...), append(insertData, baseFile[50*mb:]...)...),
		},
		{
			name: "3. Overwrite 64 KB in-place (at 50 MB) of 100 MB file",
			data: func() []byte {
				cp := make([]byte, len(baseFile))
				copy(cp, baseFile)
				copy(cp[50*mb:], insertData)
				return cp
			}(),
		},
	}

	fmt.Println("=== 100 MB File Deduplication Test (Fixed 4MB vs FastCDC 1-8MB) ===")
	for _, tc := range cases {
		fmt.Printf("\n%s\n", tc.name)

		fReused, fTotal, fPct, fUp := measureReuse(baseFixed, splitFixed(tc.data, 4*mb))
		fmt.Printf("  Fixed 4MB : %2d/%2d chunks reused | %5.1f%% bandwidth saved | Re-uploaded: %5.1f MB\n",
			fReused, fTotal, fPct, fUp)

		cReused, cTotal, cPct, cUp := measureReuse(baseCDC, splitFastCDC(tc.data))
		fmt.Printf("  FastCDC   : %2d/%2d chunks reused | %5.1f%% bandwidth saved | Re-uploaded: %5.1f MB\n",
			cReused, cTotal, cPct, cUp)
	}
}
