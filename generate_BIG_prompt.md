# Prompt: Generate BIG.md — Complete A–Z Backend & Technical Resume Interview Master Guide

---

## Context

I am a **software engineer** preparing for **Backend Engineering, System Design, and Technical Resume Deep-Dive** interviews. I have built a project on my resume that I need to explain, defend, trace at the code level, and deep-dive into during interviews. I need you to analyze my entire codebase and generate a single comprehensive file called `BIG.md` that serves as my complete A–Z interview preparation book for this project.

The file must be written as if I — the solo engineer who built, debugged, and deployed this system — am explaining it to a skeptical Senior Engineering Manager or Staff Engineer. I should be able to read this file, internalize it, and walk into any technical interview able to:
1. Deliver a crisp 90-second pitch and defend every word and number on my resume.
2. Open my repository and explain what every folder, file, and key function does.
3. Trace any user action end-to-end across frontend, API handlers, services, SQL queries, and queues.
4. Escape "chokehold" trap questions designed to test whether I actually built the project or used AI buzzwords.
5. Tell 4–5 authentic, first-person debugging "war stories" (*"Tell me about a problem you ran into and how you solved it"*).

---

## Your First Task: Read & Analyze the Entire Codebase

Before writing anything, inspect the codebase thoroughly:
- Read the directory structure, entrypoints (`cmd/`), handlers, services, domain models, database migrations/queries, background workers, and frontend state/worker files.
- Note exact file names, function names, SQL queries, table names, constants (buffer sizes, timeouts, rate limits, concurrency limits, vector dimensions), and real bug fixes in the code.

---

## Output: Generate `BIG.md` (9 Sections in Exact Order)

Write a single Markdown file with the following **nine sections**:

---

### SECTION 0: OPENING PITCH + RESUME BULLET DEFENSE
- **0.1 — The 90-Second Opening Script:** Verbatim pitch naming the problem, the tech stack and *why* each choice was made, 3 core engineering challenges solved, and an invitation to dive into any layer.
- **0.2 — Resume Bullet Defense:** For each resume bullet (plus 1–2 bonus architecture highlights not listed on the resume), provide:
  - The exact resume line
  - What was built & why it matters compared to naive approaches
  - A table of 4–5 likely follow-up/trap questions with specific, code-accurate answers

---

### SECTION 1: CODEBASE ARCHITECTURE & FILE-BY-FILE CHEAT SHEET
- **1.1 — Architecture Layering:** How to explain the separation of concerns (e.g., Clean/Hexagonal Architecture: Entrypoints → Transport/HTTP → Service/Business Logic → Domain → Repository/Storage Adapters).
- **1.2 — Annotated Directory Tree:** An ASCII tree of the repo explaining in 1 line what every important file does.
- **1.3 — "Feature-to-File" Instant Lookup Table:** A table mapping every resume feature/keyword to its **exact file path** and **key function/method name**.

---

### SECTION 2: END-TO-END CODE TRACES ("FOLLOW A REQUEST THROUGH THE CODE")
Walk through the 2–4 most important user flows step-by-step from the client action through exact handler functions, service methods, SQL queries, and background workers (e.g., *Trace 1: Upload & Deduplication*, *Trace 2: Range Streaming / Download*, *Trace 3: Real-Time Delta Sync*).

---

### SECTION 3: THE 30-MINUTE SYSTEM DESIGN WALKTHROUGH
Break a 30-minute whiteboard walkthrough into 5 timed phases (`0–5 min`, `5–12 min`, `12–20 min`, `20–26 min`, `26–30 min`). For each phase include what to say verbatim, what ASCII diagram to draw, and key design decisions to call out.

---

### SECTION 4: TECHNICAL DEEP DIVES (`4A` – `4G`)
One sub-section per major technical subsystem covering the exact math/algorithms, SQL queries, index definitions, concurrency rules, RBAC, rate limiting, and cloud infrastructure.

---

### SECTION 5: WHITEBOARD DIAGRAMS LIBRARY
Provide 4–6 clean, hand-drawable ASCII diagrams (System Architecture, Core Sequence Flow, Real-Time Sync/Event Flow, Database Schema, Permission/Auth Flow). **Use plain ASCII boxes and arrows only — no Mermaid.**

---

### SECTION 6: THE SENIOR EM "CHOKEHOLD" & TRAP QUESTIONS
Identify the **5–7 most skeptical trap questions** a Senior Engineering Manager will ask to test if the candidate is faking or reciting AI buzzwords (e.g., *"Why did you do X when the OS/database already has Y?"*, *"What happens if S3 succeeds and Postgres crashes 5ms later?"*, *"Won't this bottleneck on a 5 GB file?"*). Provide the exact first-principles answer that passes.

---

### SECTION 7: REAL-LIFE DEBUGGING WAR STORIES ("EXPLAIN A SCENARIO WHERE YOU FACED A PROBLEM AND SOLVED IT")
Write **5 concrete, real-life debugging stories** pulled from actual edge cases in the codebase. Format each as a natural, first-person story covering:
1. **What I was testing / doing** (e.g., testing in Chrome DevTools, deploying to EC2, uploading from two tabs at once).
2. **What broke on my screen / in the logs** (the exact symptom or error code like `HTTP 429`, `HTTP 500 unique constraint`, `403 InvalidClientTokenId`, duplicate WebSocket events, or hash divergence on byte 64).
3. **How I investigated it** (what I printed, inspected, or traced).
4. **The exact code fix I wrote** and what I learned.

---

### SECTION 8: BACKEND Q&A BANK, BEHAVIORAL SCRIPTS & 1-PAGE NUMBERS CHEAT SHEET
- **8.1 — Backend Technical Q&A:** Database, Concurrency, Distributed Systems, and Language/API design questions.
- **8.2 — Project Openers & Behavioral Scripts:** *"Why did you build this?"*, *"What would you add next?"* (must NOT list features already built!), and *"How did you validate correctness working solo?"*
- **8.3 — 1-Page Numbers & Constants Cheat Sheet:** A table of every single metric, buffer size, rate limit, timeout, and benchmark number in the project with a simple, 1-sentence explanation that a solo developer can own 100% on the spot.
