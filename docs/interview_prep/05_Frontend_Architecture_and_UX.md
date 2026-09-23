# 05. Frontend Architecture & User Experience

## 1. Frontend Technology Stack & System Design

- **Framework**: React 18 + TypeScript.
- **Build Tool**: Vite (Lightning-fast HMR and optimized ESM bundling).
- **Styling Engine**: TailwindCSS + Custom Slate Architectural Tokens ([`frontend_change.md`](file:///c:/Users/Asus/Desktop/z/frontend_change.md)).
- **HTTP Client**: Axios with automated response interceptors for token refresh rotation.

---

## 2. "Precision Architectural Slate" UI Design Token Matrix

To create a high-contrast, editorial drive interface that removes generic template aesthetic tropes, the frontend enforces a strict visual system:

### Typography Specification
- **Display & Logotype**: `'Syne'` (Weights: 700, 800) for page headers, logotype, and branding.
- **Interface & Body**: `'Plus Jakarta Sans'` for inputs, buttons, and system controls.
- **Monospaced Technical Metadata**: `'JetBrains Mono'` for file sizes, dates, SHA-256 hashes, IP addresses, and shortcut keys (`⌘K`, `/`).

### Locked Color Matrix
- **Backdrop Canvas (`bg-arch-950`)**: `#090a0c`
- **Surface / Panel (`bg-arch-900`)**: `#111317`
- **Hover Surface (`bg-arch-850`)**: `#15181e`
- **1px Rule Boundary (`border-arch-border`)**: `#242830`
- **Primary CTA Accent (`bg-amber-500`)**: `#f59e0b`

---

## 3. Web Worker Multithread Hashing (`hash.worker.ts`)

### The Performance Problem
Computing SHA-256 hashes for large files (e.g. 500MB split into 125 x 4MB blocks) on the browser main UI thread blocks the JavaScript Event Loop. This causes frozen CSS animations, non-responsive buttons, and browser lag ("Page Unresponsive" popups).

### Web Worker Solution ([hash.worker.ts](file:///c:/Users/Asus/Desktop/z/frontend/src/workers/hash.worker.ts))
1. File slicing and hashing logic is offloaded to a dedicated Web Worker running on a secondary OS thread.
2. Web Worker reads file chunks via `FileReaderSync` or `Blob.arrayBuffer()`.
3. Hashes 4MB chunks sequentially using the Web Crypto API (`crypto.subtle.digest('SHA-256', buffer)`).
4. Returns array of hash strings to main thread without causing a single dropped frame (60 FPS main thread performance).

---

## 4. Component Structure & User Workflows

```
frontend/src/
├── workers/
│   └── hash.worker.ts         # Dedicated SHA-256 block hashing Web Worker
├── components/
│   ├── ListView.tsx           # Monospaced tabular file explorer view
│   ├── GridView.tsx           # High-density card file explorer view
│   ├── ContextMenu.tsx        # Custom right-click floating actions menu
│   ├── BulkActionBar.tsx      # Floating bottom action bar for multi-select
│   ├── FilePreviewModal.tsx   # Live modal preview (Images, Video, PDFs, Code)
│   ├── UploadQueue.tsx        # Real-time upload queue progress drawer
│   ├── MoveModal.tsx          # Drag-and-drop & folder selection tree modal
│   ├── ShareModal.tsx         # User permission sharing modal
│   └── ActiveSessionsModal.tsx# Device inspector & remote session revocation
└── pages/
    ├── Dashboard.tsx          # Core Drive Explorer Dashboard
    ├── Login.tsx              # Asymmetrical split-screen auth
    └── Register.tsx           # Account registration flow
```

---

## 5. Trade-Offs & Architectural Alternatives

| Approach | Implemented in Blob-Cloud | Alternative Approach | Why We Chose Our Approach |
| :--- | :--- | :--- | :--- |
| **Application Model** | **Single Page Application (Vite)** | Next.js Server-Side Rendering (SSR) | Blob-Cloud is a private, authenticated SaaS app; SSR adds unnecessary node server hosting costs with zero SEO benefit for authenticated dashboards. |
| **Hashing Threads** | **Dedicated Web Worker Multithreading** | Main Thread Synchronous Hashing | Offloading hashing prevents DOM freeze and guarantees 60 FPS UI performance during massive batch uploads. |
| **State Management** | **React Context + Local Storage Hooks** | Redux Toolkit / Zustand | Dashboard state (current directory, selection, upload progress) is localized cleanly without Redux boilerplate overhead. |

---

## 6. Interviewer Deep-Dive Q&A

### Q1: How do you handle automatic JWT Token Refresh on the frontend?
**Answer**:
We configure an Axios Response Interceptor ([`api.ts`](file:///c:/Users/Asus/Desktop/z/frontend/src/lib/api.ts)). When any API call returns `401 Unauthorized`:
1. The interceptor pauses outgoing requests and triggers `POST /api/auth/refresh` (sending the HTTP-Only refresh cookie).
2. If successful, it receives a new `access_token`, updates the Authorization header, and retries the original failed request seamlessly without logging the user out.

### Q2: How does the client handle drag-and-drop uploads of entire nested folder structures?
**Answer**:
We listen to `onDrop` events using the HTML5 `DataTransferItem.webkitGetAsEntry()` API. We recursively traverse `FileSystemDirectoryEntry` and `FileSystemFileEntry` trees to extract files while preserving their relative paths (`webkitRelativePath`), constructing missing target folders automatically before initiating session uploads.
