/**
 * AIMD Adaptive Upload Concurrency Controller
 *
 * Implements a TCP-inspired Additive Increase / Multiplicative Decrease (AIMD)
 * algorithm to self-tune the number of parallel chunk upload streams at runtime.
 *
 * Cold-start: reads navigator.connection (Network Information API) to pick an
 * informed initial concurrency based on the user's measured effective connection
 * type and downlink speed, rather than defaulting to a static constant.
 *
 * Steady-state adaptation:
 *   - Additive Increase (AI): when CONSECUTIVE_GOOD consecutive chunk completions
 *     each show ≥ AI_THRESHOLD throughput improvement over the previous sample,
 *     concurrency is bumped by 1 (up to MAX_CONCURRENCY). Slow and steady — we
 *     don't overshoot on a single lucky chunk.
 *   - Multiplicative Decrease (MD): on a hard error (all retries exhausted) OR on
 *     a timeout signal (chunk took > TIMEOUT_MULTIPLIER × median window duration),
 *     concurrency is halved (floor). Fast reaction — mirrors TCP's behaviour on
 *     detecting congestion.
 *
 * Design constraints:
 *   - One controller instance per file upload (not shared across files).
 *   - All logging is gated behind import.meta.env.DEV (zero noise in production).
 *   - No React dependency — plain TypeScript class.
 */

const MIN_CONCURRENCY = 1
const MAX_CONCURRENCY = 8

/** Number of recent chunk samples kept in the sliding window. */
const WINDOW_SIZE = 4

/**
 * Minimum fractional throughput improvement (5%) over the previous sample
 * required to count as a "good" signal for additive increase.
 */
const AI_THRESHOLD = 0.05

/**
 * Number of consecutive improving samples required before concurrency is
 * incremented. Prevents a single fast chunk from inflating parallelism.
 */
const CONSECUTIVE_GOOD = 2

/**
 * If a chunk's PUT duration exceeds this multiple of the window's median
 * duration, it is treated as a congestion signal even if it eventually
 * succeeded (i.e. no hard error). This mirrors TCP's RTO timeout heuristic.
 */
const TIMEOUT_MULTIPLIER = 2

interface ThroughputSample {
  /** bytes / ms — effective throughput for this chunk's PUT. */
  throughput: number
  /** Wall-clock duration of the successful PUT request in milliseconds. */
  durationMs: number
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/** Return the median value of a sorted numeric array. */
function median(sorted: number[]): number {
  const mid = Math.floor(sorted.length / 2)
  return sorted.length % 2 !== 0
    ? sorted[mid]
    : (sorted[mid - 1] + sorted[mid]) / 2
}

// ---------------------------------------------------------------------------
// Controller
// ---------------------------------------------------------------------------

export class AIMDConcurrencyController {
  private concurrency: number
  private window: ThroughputSample[] = []
  private consecutiveImprovements = 0
  private readonly dev: boolean = import.meta.env.DEV

  constructor() {
    this.concurrency = this.coldStart()
  }

  // -------------------------------------------------------------------------
  // Cold-start calibration via navigator.connection (Network Information API)
  // -------------------------------------------------------------------------

  private coldStart(): number {
    try {
      // The Network Information API is non-standard; cast defensively.
      const conn =
        (navigator as unknown as { connection?: NetworkInformation }).connection ??
        (navigator as unknown as { mozConnection?: NetworkInformation }).mozConnection ??
        (navigator as unknown as { webkitConnection?: NetworkInformation }).webkitConnection

      if (!conn) {
        this.log('cold-start: navigator.connection unavailable → default concurrency=3')
        return 3
      }

      const effectiveType: string = (conn as { effectiveType?: string }).effectiveType ?? '4g'
      // downlink is in Mbps; may be 0 on some browsers when unavailable.
      const downlink: number = (conn as { downlink?: number }).downlink ?? 0

      let initial: number
      if (effectiveType === 'slow-2g' || effectiveType === '2g') {
        initial = 1
      } else if (effectiveType === '3g' || (downlink > 0 && downlink < 5)) {
        initial = 2
      } else if (downlink > 0 && downlink < 10) {
        initial = 3
      } else if (downlink > 0 && downlink < 20) {
        initial = 4
      } else {
        // 4g / unknown / fast WiFi
        initial = 6
      }

      this.log(
        `cold-start: effectiveType=${effectiveType}, downlink=${downlink > 0 ? `${downlink}Mbps` : 'unknown'} → initial concurrency=${initial}`,
      )
      return initial
    } catch {
      this.log('cold-start: error reading navigator.connection → default concurrency=3')
      return 3
    }
  }

  // -------------------------------------------------------------------------
  // Public API
  // -------------------------------------------------------------------------

  /** Current target number of concurrent chunk upload streams. */
  getConcurrency(): number {
    return this.concurrency
  }

  /**
   * Feed the result of a successful chunk PUT into the controller.
   *
   * @param bytes      - Size of the uploaded chunk payload in bytes.
   * @param durationMs - Wall-clock duration of the successful PUT in ms.
   *                     Must NOT include retry backoff waits — only the
   *                     successful network round-trip.
   */
  onChunkComplete(bytes: number, durationMs: number): void {
    const throughput = durationMs > 0 ? bytes / durationMs : bytes
    const sample: ThroughputSample = { throughput, durationMs }

    // Maintain sliding window.
    this.window.push(sample)
    if (this.window.length > WINDOW_SIZE) this.window.shift()

    const mbps = ((throughput * 1000) / (1024 * 1024)).toFixed(2)

    // ------------------------------------------------------------------
    // Timeout / congestion heuristic (Multiplicative Decrease)
    // Requires at least 2 samples so we have a meaningful median to compare
    // against. We exclude the current sample from the median calculation.
    // ------------------------------------------------------------------
    if (this.window.length >= 2) {
      const historicDurations = this.window.slice(0, -1).map((s) => s.durationMs).sort((a, b) => a - b)
      const medianDuration = median(historicDurations)

      if (medianDuration > 0 && durationMs > TIMEOUT_MULTIPLIER * medianDuration) {
        const before = this.concurrency
        this.concurrency = Math.max(MIN_CONCURRENCY, Math.floor(this.concurrency / 2))
        this.consecutiveImprovements = 0
        this.log(
          `timeout signal: chunk took ${durationMs.toFixed(0)}ms > ${TIMEOUT_MULTIPLIER}× median ` +
          `${medianDuration.toFixed(0)}ms → multiplicative decrease: ${before} → ${this.concurrency} streams`,
        )
        return
      }
    }

    // ------------------------------------------------------------------
    // Additive Increase check
    // ------------------------------------------------------------------
    if (this.window.length >= 2) {
      const prevThroughput = this.window[this.window.length - 2].throughput
      const improvement = prevThroughput > 0 ? (throughput - prevThroughput) / prevThroughput : 0

      if (improvement >= AI_THRESHOLD) {
        this.consecutiveImprovements++
      } else {
        this.consecutiveImprovements = 0
      }

      if (this.consecutiveImprovements >= CONSECUTIVE_GOOD && this.concurrency < MAX_CONCURRENCY) {
        const before = this.concurrency
        this.concurrency = Math.min(MAX_CONCURRENCY, this.concurrency + 1)
        this.consecutiveImprovements = 0
        this.log(
          `additive increase: ${before} → ${this.concurrency} streams ` +
          `(throughput +${(improvement * 100).toFixed(1)}% over ${CONSECUTIVE_GOOD} consecutive chunks, ${mbps} MB/s)`,
        )
        return
      }
    }

    // Normal sample — log throughput without state change.
    this.log(
      `chunk complete: ${(bytes / (1024 * 1024)).toFixed(2)}MB in ${durationMs.toFixed(0)}ms → ` +
      `${mbps} MB/s | concurrency=${this.concurrency} (consecutiveGood=${this.consecutiveImprovements}/${CONSECUTIVE_GOOD})`,
    )
  }

  /**
   * Signal that a chunk upload failed after all retries were exhausted.
   * Applies Multiplicative Decrease unconditionally.
   */
  onChunkError(): void {
    const before = this.concurrency
    this.concurrency = Math.max(MIN_CONCURRENCY, Math.floor(this.concurrency / 2))
    this.consecutiveImprovements = 0
    this.log(`chunk error (all retries exhausted) → multiplicative decrease: ${before} → ${this.concurrency} streams`)
  }

  // -------------------------------------------------------------------------
  // Private helpers
  // -------------------------------------------------------------------------

  private log(msg: string): void {
    if (this.dev) {
      // eslint-disable-next-line no-console
      console.info(`%c[AIMD]%c ${msg}`, 'color:#7c3aed;font-weight:bold', 'color:inherit')
    }
  }
}

// Extend the Navigator interface to account for the non-standard connection API
// without importing a separate types package.
interface NetworkInformation extends EventTarget {
  effectiveType?: string
  downlink?: number
  rtt?: number
  saveData?: boolean
}
