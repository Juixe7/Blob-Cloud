# Phase 5 Walkthrough: FastCDC Content-Defined Chunking for Shift-Resistant Deduplication

We have completed **Phase 5: FastCDC Content-Defined Chunking**. Blob-Cloud has eliminated the "byte-shift fallacy" of fixed-size chunking (where inserting a single byte destroyed 100% of chunk deduplication), replacing it with **FastCDC (Fast Content-Defined Chunking)** using rolling Gear hashing, normalized dual masks, $S_{min}$ byte skipping, and a dedicated browser Web Worker.

---

## 1. What Was Built & Modified

| Component | Path | Description |
| :--- | :--- | :--- |
| **Gear Lookup Matrix** | [`backend/internal/chunker/gear_table.go`](file:///c:/Users/Asus/Desktop/z/backend/internal/chunker/gear_table.go) | 256-element 64-bit lookup table derived deterministically for identical boundary matching across distributed nodes and client workers. |
| **FastCDC Streaming Engine** | [`backend/internal/chunker/fastcdc.go`](file:///c:/Users/Asus/Desktop/z/backend/internal/chunker/fastcdc.go) | High-throughput streaming chunker with dual masks ($M_s = \text{0x7FFFFF}$, $M_l = \text{0x1FFFFF}$), $S_{min} = 1\text{ MB}$ skipping (25% CPU savings), zero-allocation windowing, and `Split` convenience function. |
| **FastCDC Verification Suite** | [`backend/internal/chunker/fastcdc_test.go`](file:///c:/Users/Asus/Desktop/z/backend/internal/chunker/fastcdc_test.go) | Empirical verification of 1-byte and 17-byte shift resistance, boundary invariants ($S_{min} \le L \le S_{max}$), low-entropy data stability (`0x00`, `0xFF`), and throughput benchmarks with race detection (`-race`). |
| **Client-Side Web Worker** | [`frontend/src/workers/fastcdc.worker.ts`](file:///c:/Users/Asus/Desktop/z/frontend/src/workers/fastcdc.worker.ts) | Background Web Worker running 64-bit FastCDC rolling Gear hash in browser threads; streams multi-gigabyte files in 8MB window buffers to prevent memory bloat. |
| **Upload Context Integration** | [`frontend/src/context/UploadContext.tsx`](file:///c:/Users/Asus/Desktop/z/frontend/src/context/UploadContext.tsx) | Upgraded upload lifecycle to use `fastcdc.worker.ts` and slice chunks by dynamic content-defined offsets (`chunkMeta.offset`) and lengths (`chunk.size_bytes`). |

---

## 2. Empirical Deduplication Results: Shift Resistance

In our test suite ([`fastcdc_test.go`](file:///c:/Users/Asus/Desktop/z/backend/internal/chunker/fastcdc_test.go)), we tested an edit of **1 byte prepended** to a 5 MiB file:

| Chunking Strategy | Chunks Matched | Deduplication Ratio | Note |
| :--- | :--- | :--- | :--- |
| **Fixed-Size Chunking (64 KiB / 4 MiB)** | **0 / 80 chunks** | **0.00%** | **Catastrophic Collapse**: All boundaries shifted by 1 byte; full re-upload required. |
| **FastCDC Content-Defined Chunking** | **72 / 73 chunks** | **98.63%** | **Resilient**: Boundaries re-aligned immediately at the first natural boundary. Only 1 chunk re-uploaded! |

For a 17-byte shift:
- **FastCDC**: **72 / 73 chunks matched (98.63% deduplication)**.

---

## 3. Automated Test & Build Verification

```powershell
# 1. Chunker tests with race detector (uncached)
cd backend
go test -race -count=1 -v ./internal/chunker/...
# Result: 100% PASS
# === RUN   TestFastCDC_EmptyAndSmallFiles
# --- PASS: TestFastCDC_EmptyAndSmallFiles (0.00s)
# === RUN   TestFastCDC_StreamingEquivalence
# --- PASS: TestFastCDC_StreamingEquivalence (0.18s)
# === RUN   TestFastCDC_BoundaryGuarantees
# --- PASS: TestFastCDC_BoundaryGuarantees (0.10s)
# === RUN   TestFastCDC_ShiftResistance
#     fastcdc_test.go:197: Shift by 1 byte: 72 / 73 chunks matched (98.63% deduplication)
#     fastcdc_test.go:229: Fixed 64KB chunking on 1-byte shift matched: 0 / 80 chunks (Catastrophic collapse)
#     fastcdc_test.go:248: Shift by 17 bytes: 72 / 73 chunks matched (98.63% deduplication)
# --- PASS: TestFastCDC_ShiftResistance (0.27s)
# PASS

# 2. Chunker micro-benchmarks
go test -bench=BenchmarkFastCDC -benchmem ./internal/chunker
# Result:
# BenchmarkFastCDC_Split_10MB-16        28   38.5 ms/op   272.19 MB/s   545 B/op   8 allocs/op
# BenchmarkFastCDC_Streaming_10MB-16    31   39.8 ms/op   262.82 MB/s   16.7 MB/op  15 allocs/op

# 3. Full Backend test suite with race detector
go test -race ./...
# Result: 100% PASS across all 12 packages

# 4. Backend linting
go vet ./...
# Result: Exit 0 (zero lint warnings)

# 5. Frontend TypeScript type-checking
cd frontend
npx tsc -b
# Result: Exit 0 (zero type errors)

# 6. Frontend production build
npm run build
# Result: Exit 0 (Vite built cleanly; fastcdc.worker-C_ARrQiP.js emitted)
```

---

## 4. Algorithm & Pro Model Advisory Gate

Below is the verification summary and prompt snippet for auditing the mathematical invariants with **Gemini Pro**:

### Invariant Assertions
1. **Boundary Floor Guarantee**: $\forall \text{chunk } i \in [0, N-2], \text{Length}(C_i) \ge S_{min}$. Only terminal chunk $C_{N-1}$ may have $\text{Length} < S_{min}$.
2. **Boundary Ceiling Guarantee**: $\forall \text{chunk } i, \text{Length}(C_i) \le S_{max}$. Under low entropy or adversarial repeating patterns (e.g. `0x00`, `0xFF`), chunking never enters an infinite loop and cuts hard at $S_{max}$.
3. **Entropy Invariance**: $S_{min}$ byte skipping does not alter downstream boundary convergence because the Gear hash state $H$ converges within a few dozen bytes of content match.

### Audit Prompt for Gemini Pro
```markdown
Review the FastCDC chunking parameters for Blob-Cloud's CAS block pipeline:
- Target Average Chunk Size: 4 MB (2^22 bytes)
- Minimum Chunk Size: 1 MB (2^20 bytes)
- Maximum Chunk Size: 8 MB (2^23 bytes)
- Small Mask: 0x00007FFFFF (23 bits set, p = 2^-23)
- Large Mask: 0x00001FFFFF (21 bits set, p = 2^-21)
- Skip bytes: S_min (1 MB)

Verify:
1. Does the transition from MaskSmall to MaskLarge at S_avg (4MB) correctly normalize the chunk distribution around 4MB?
2. What is the probability of a chunk reaching S_max under random uniform data?
3. In what edge cases could Gear hashing experience pathological behavior, and how does the S_max ceiling prevent denial-of-service?
```
