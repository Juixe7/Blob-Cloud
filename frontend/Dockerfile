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

# Declare build arguments so Docker Compose and CI/CD can inject
# environment variables at image build time
ARG VITE_API_BASE=/api
ARG VITE_GOOGLE_CLIENT_ID
ENV VITE_API_BASE=$VITE_API_BASE
ENV VITE_GOOGLE_CLIENT_ID=$VITE_GOOGLE_CLIENT_ID

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
