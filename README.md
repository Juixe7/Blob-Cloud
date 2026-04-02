# TrulyLied ☁️

**Live Demo:** [https://trulylied.vercel.app](https://trulylied.vercel.app)

A modern, high-performance file storage and sharing application. TrulyLied provides a seamless, secure, and fast way to upload, manage, and share your files across devices.

## Features ✨

- **Secure File Storage:** Robust backend architecture ensuring your data is stored safely.
- **Fast Uploads:** Supports chunked uploads and efficient background processing.
- **File Previews:** Built-in preview capabilities for various file types right in the browser.
- **Public & Private Sharing:** Generate shareable links for public access or restrict them to specific permissions.
- **Real-time Synchronization:** Built with WebSockets to keep file states in sync across multiple active sessions.
- **Responsive UI:** A beautiful, intuitive frontend interface built with modern web technologies.

## Tech Stack 🛠️

**Frontend:**
- React + TypeScript
- Vite
- TailwindCSS
- Context API for State Management

**Backend:**
- Go (Golang)
- PostgreSQL (Database)
- Amazon S3 (Object Storage)
- WebSockets for Real-time events

## Getting Started 🚀

### Prerequisites
- Go 1.20+
- Node.js 18+
- PostgreSQL
- S3-compatible storage (AWS S3, MinIO, etc.)

### Backend Setup
1. Navigate to the `backend` directory.
2. Copy the environment file and fill in your database/S3 credentials (if applicable).
3. Run the database migrations.
4. Start the server:
   ```bash
   go run cmd/api/main.go
   ```

### Frontend Setup
1. Navigate to the `frontend` directory.
2. Install dependencies:
   ```bash
   npm install
   ```
3. Start the development server:
   ```bash
   npm run dev
   ```

## License 📄
This project is licensed under the MIT License.
