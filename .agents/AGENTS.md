# Production & System Design Guidelines (Resume/Interview Standard)

- **System Design & Architecture**: Maintain strict separation of concerns across layers (HTTP Transport, Application/Service Layer, Domain Models, Repository Layer, and Storage Providers).
- **RESTful Protocols & HTTP Specs**: Adhere strictly to RFC HTTP standards (e.g. Range Requests with HTTP 206 Partial Content, Content-Range, Content-Length, CORS Expose Headers, RESTful URL design).
- **Clean Code & Type Safety**: Ensure zero-warning Go and TypeScript code compilation (`go build ./...`, `npx tsc -b`, `vite build`).
- **Resilience & Resource Management**: Always handle context cancellations (`ctx.Done()`), cleanup file/network handles immediately (`defer rc.Close()`), and handle edge cases gracefully.
- **UI/UX Aesthetics**: Provide top-tier UI design, reactive states, loading skeletons, error toasts, and responsive user feedback.
