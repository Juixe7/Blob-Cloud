# ==============================================================================
# Multi-Stage Production Dockerfile for Blob-Cloud Frontend SPA
# Stage 1: Build the production bundle using Node 20
# Stage 2: Serve static bundle & reverse-proxy /api via Nginx 1.27
# ==============================================================================

FROM node:20-alpine AS builder

WORKDIR /app

# Cache dependency layer
COPY package.json package-lock.json ./
RUN npm ci

# Copy frontend source
COPY . .

# TypeScript check + Vite production bundle
RUN npm run build

# ------------------------------------------------------------------------------
# Production Runner: Alpine Nginx
# ------------------------------------------------------------------------------
FROM nginx:1.27-alpine AS runner

# Remove default files
RUN rm -rf /usr/share/nginx/html/*

# Copy compiled production bundle
COPY --from=builder /app/dist /usr/share/nginx/html

# Deploy custom Nginx reverse proxy configuration
COPY nginx.conf /etc/nginx/conf.d/default.conf

EXPOSE 80

CMD ["nginx", "-g", "daemon off;"]
