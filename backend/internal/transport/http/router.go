package httpx

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go-drive-clone/internal/metrics"
	"go-drive-clone/internal/ratelimit"
)

// RateLimiters bundles the three zone limiters the router applies. Any nil
// limiter disables rate limiting for that zone (safe default for unit tests).
type RateLimiters struct {
	Auth   ratelimit.Limiter
	Upload ratelimit.Limiter
	API    ratelimit.Limiter

	AuthCfg   ratelimit.ZoneConfig
	UploadCfg ratelimit.ZoneConfig
	APICfg    ratelimit.ZoneConfig
}


// corsMiddleware allows the browser frontend (served on a different origin/port
// in development) to issue direct PUT uploads to this server. It mirrors what
// S3 CORS configuration will do in Phase 4.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, PUT, POST, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.Header().Set("Access-Control-Expose-Headers", "Content-Length, Content-Range, X-Directory-Deleted")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// NewRouter builds the chi router, wires middleware, and mounts routes onto the
// provided Server. rl carries the per-zone rate limiters; pass a zero-value
// RateLimiters{} (all nil) to disable rate limiting (e.g. in unit tests).
func NewRouter(s *Server, rl RateLimiters) http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(corsMiddleware)
	r.Use(metrics.PrometheusMiddleware)

	// Prometheus metrics endpoint — not behind auth, accessed by scraper only.
	r.Get("/metrics", metrics.Handler().ServeHTTP)

	// --- Phase 1: storage simulation + health ---
	r.Get("/health", s.HandleHealth)
	r.Put("/local-storage/blocks/{hash}", s.HandlePutBlock)

	// --- Auth endpoints — strict rate limit (brute-force / credential stuffing) ---
	r.Route("/api/auth", func(r chi.Router) {
		if rl.Auth != nil {
			r.Use(ratelimit.Middleware(rl.Auth, rl.AuthCfg))
		}
		r.Post("/register", s.HandleRegister)
		r.Post("/login", s.HandleLogin)
		r.Post("/refresh", s.HandleRefresh)
		r.Post("/verify", s.HandleVerify)
		r.Post("/resend-verification", s.HandleResendVerification)
		r.Post("/google", s.HandleGoogleOAuth)
		r.Post("/forgot-password", s.HandleForgotPassword)
		r.Post("/reset-password", s.HandleResetPassword)
		r.Post("/change-password", s.HandleChangePassword)
		r.Post("/logout", s.HandleLogout)
	})

	// --- General API — generous rate limit ---
	r.Group(func(r chi.Router) {
		if rl.API != nil {
			r.Use(ratelimit.Middleware(rl.API, rl.APICfg))
		}

		// --- Directory listing, folder creation, & user metrics ---
		r.Post("/api/folders", s.HandleCreateFolder)
		r.Get("/api/user/storage", s.HandleGetUserStorage)
		r.Route("/api/user/sessions", func(r chi.Router) {
			r.Get("/", s.HandleListSessions)
			r.Post("/revoke", s.HandleRevokeSession)
			r.Post("/revoke-all", s.HandleRevokeAllOtherSessions)
		})

		// --- Phase 3: resumable uploads — moderate rate limit ---
		r.Route("/api/upload", func(r chi.Router) {
			if rl.Upload != nil {
				r.Use(ratelimit.Middleware(rl.Upload, rl.UploadCfg))
			}
			r.Post("/initiate", s.HandleInitiateUpload)
			r.Get("/session/{id}", s.HandleGetSession)
			r.Post("/complete", s.HandleCompleteUpload)
		})

		// --- Phase 3: sharing & permissions / Phase 7.4: file operations ---
		r.Route("/api/files", func(r chi.Router) {
			r.Get("/", s.HandleListFiles)
			r.Get("/download", s.HandleDownload)
			r.Get("/trash", s.HandleListTrash)

			// Phase C.1: Bulk Operations
			r.Post("/bulk/delete", s.HandleBulkSoftDelete)
			r.Post("/bulk/restore", s.HandleBulkRestore)
			r.Post("/bulk/move", s.HandleBulkMove)
			r.Post("/shortcut", s.HandleCreateShortcut)
			r.Delete("/bulk/permanent", s.HandleBulkHardDelete)

			r.Post("/{id}/share", s.HandleShare)
			r.Get("/{id}/permissions", s.HandleListPermissions)
			r.Patch("/{id}", s.HandleRenameMove)
			r.Delete("/{id}", s.HandleDelete)
			r.Post("/{id}/restore", s.HandleRestore)
			r.Delete("/{id}/permanent", s.HandlePermanentDelete)
			r.Get("/{id}/download", s.HandleDownload)
			r.Get("/{id}/thumbnail", s.HandleGetThumbnail)
			r.Get("/{id}/history", s.HandleFileHistory)   // Tier 2F: audit trail
		})

		// --- Phase 6: real-time notifications (WebSocket) ---
		if s.hub != nil {
			r.Get("/api/ws", s.HandleWSConnection)
		}
	})

	return r
}
