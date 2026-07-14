package httpx

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// corsMiddleware allows the browser frontend (served on a different origin/port
// in development) to issue direct PUT uploads to this server. It mirrors what
// S3 CORS configuration will do in Phase 4.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, PUT, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// NewRouter builds the chi router, wires middleware, and mounts routes onto the
// provided Server (which carries the injected dependencies).
func NewRouter(s *Server) http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(corsMiddleware)

	// --- Phase 1: storage simulation + health ---
	r.Get("/health", s.HandleHealth)
	r.Put("/local-storage/blocks/{hash}", s.HandlePutBlock)

	// --- Phase 3: resumable uploads (Upgrade A) ---
	r.Route("/api/upload", func(r chi.Router) {
		r.Post("/initiate", s.HandleInitiateUpload)
		r.Get("/session/{id}", s.HandleGetSession)
		r.Post("/complete", s.HandleCompleteUpload)
	})

	// --- Phase 3: sharing & permissions (Upgrade B) ---
	r.Route("/api/files", func(r chi.Router) {
		r.Post("/{id}/share", s.HandleShare)
		r.Get("/{id}/permissions", s.HandleListPermissions)
	})

	// --- Phase 6: real-time notifications (WebSocket) ---
	if s.hub != nil {
		r.Get("/api/ws", s.HandleWSConnection)
	}

	return r
}
