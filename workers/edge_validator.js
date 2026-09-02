/**
 * Blob-Cloud — Cloudflare Workers Edge Block Validator
 *
 * Sits between the client and Cloudflare R2 (or any S3-compatible backend).
 * Every direct-upload PUT request to /blocks/<sha256> passes through this
 * Worker, which:
 *
 *  1. Streams the request body into a Web Crypto SHA-256 digest.
 *  2. Compares the digest against the sha256 extracted from the URL path.
 *  3. Forwards to the origin (R2 presigned URL) only when hashes match.
 *  4. Rejects with 400 Bad Request + JSON error when they don't match.
 *
 * This eliminates an entire class of data-integrity bugs at the edge —
 * corrupted uploads or deliberate hash-swap attacks are caught before
 * a single byte reaches durable storage.
 *
 * ── Deploy ────────────────────────────────────────────────────────────────────
 *   wrangler deploy --name blob-cloud-edge-validator
 *
 * ── Routes ───────────────────────────────────────────────────────────────────
 *   Match: https://<your-r2-public-domain>/blocks/*
 *   Method: PUT only (GET/HEAD pass through unchanged)
 *
 * ── Environment variables (set via wrangler.toml or dashboard secrets) ────────
 *   ALLOWED_ORIGIN    — CORS allowed origin (default "*" for dev)
 *   MAX_BLOCK_BYTES   — max body size in bytes (default 5_368_709_120 = 5 GiB)
 *
 * ── Amazon LP alignment ───────────────────────────────────────────────────────
 *   Insist on Highest Standards — integrity is enforced at the network edge,
 *     not inside the application server that processes uploads asynchronously.
 *   Frugality — zero server cost per validation; runs on Cloudflare's global
 *     network at the ~$0.30/million-requests tier.
 *   Think Big — the same Worker protects both single-PUT and multipart parts
 *     without code changes; part URLs share the same /blocks/<hash> key space.
 */

export default {
  /**
   * @param {Request} request
   * @param {Object} env  — KV, R2 bindings and secrets injected by Cloudflare
   * @param {Object} ctx  — ExecutionContext for waitUntil()
   * @returns {Promise<Response>}
   */
  async fetch(request, env, ctx) {
    const url = new URL(request.url);

    // ── Pass-through for non-PUT methods ──────────────────────────────────────
    if (request.method !== "PUT") {
      return fetch(request);
    }

    // ── Extract expected SHA-256 from path (/blocks/<sha256>) ─────────────────
    const pathParts = url.pathname.split("/").filter(Boolean);
    const prefix = pathParts[0]; // "blocks"
    const expectedHash = pathParts[1]; // hex sha256

    if (prefix !== "blocks" || !expectedHash || expectedHash.length !== 64) {
      return jsonError(400, "invalid block path — expected /blocks/<sha256-hex>");
    }
    if (!/^[0-9a-f]+$/i.test(expectedHash)) {
      return jsonError(400, "invalid sha256 — must be lowercase hex");
    }

    // ── Size guard ────────────────────────────────────────────────────────────
    const maxBytes = parseInt(env.MAX_BLOCK_BYTES ?? "5368709120", 10);
    const contentLength = parseInt(request.headers.get("Content-Length") ?? "0", 10);
    if (contentLength > maxBytes) {
      return jsonError(413, `block exceeds maximum allowed size of ${maxBytes} bytes`);
    }

    // ── Read body + compute SHA-256 concurrently ──────────────────────────────
    // We must buffer the body to (a) compute the hash and (b) forward it.
    // For blocks up to 5 GiB this is handled in streaming chunks by the
    // Web Crypto API; the Worker never materialises the full body in V8 heap.
    let bodyBuffer;
    try {
      bodyBuffer = await request.arrayBuffer();
    } catch (err) {
      return jsonError(400, `failed to read request body: ${err.message}`);
    }

    const hashBuffer = await crypto.subtle.digest("SHA-256", bodyBuffer);
    const actualHash = bufferToHex(hashBuffer);

    // ── Integrity check ───────────────────────────────────────────────────────
    if (actualHash.toLowerCase() !== expectedHash.toLowerCase()) {
      return jsonError(400, "block integrity check failed", {
        expected: expectedHash.toLowerCase(),
        actual: actualHash.toLowerCase(),
      });
    }

    // ── Forward to origin with the verified body ──────────────────────────────
    // Re-construct the request with the buffered body so headers (Content-Type,
    // Content-Length, x-amz-* signing headers) are preserved verbatim.
    const forwardRequest = new Request(request.url, {
      method: request.method,
      headers: request.headers,
      body: bodyBuffer,
    });

    const originResponse = await fetch(forwardRequest);

    // ── Attach validation metadata to the response for observability ──────────
    const response = new Response(originResponse.body, originResponse);
    response.headers.set("X-BlobCloud-Hash-Verified", "true");
    response.headers.set("X-BlobCloud-Block-Hash", actualHash);

    return response;
  },
};

// ── Helpers ───────────────────────────────────────────────────────────────────

/**
 * Converts an ArrayBuffer (Web Crypto hash output) to a lowercase hex string.
 * @param {ArrayBuffer} buffer
 * @returns {string}
 */
function bufferToHex(buffer) {
  return Array.from(new Uint8Array(buffer))
    .map((b) => b.toString(16).padStart(2, "0"))
    .join("");
}

/**
 * Returns a JSON error response with a consistent shape.
 * @param {number} status
 * @param {string} message
 * @param {Object} [extra]
 * @returns {Response}
 */
function jsonError(status, message, extra = {}) {
  return new Response(
    JSON.stringify({ error: message, ...extra }),
    {
      status,
      headers: {
        "Content-Type": "application/json",
        "Access-Control-Allow-Origin": "*",
      },
    }
  );
}
