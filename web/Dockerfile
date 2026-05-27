ARG NODE_IMAGE=node:20-alpine
ARG NGINX_IMAGE=nginx:1.27-alpine

FROM ${NODE_IMAGE} AS builder

WORKDIR /web

COPY package*.json ./
RUN npm ci

COPY . .
RUN npm run build

FROM ${NGINX_IMAGE}

ENV BACKEND_UPSTREAM=chat2db-server:8080

COPY nginx/default.conf.template /etc/nginx/templates/default.conf.template
COPY --from=builder /web/dist /usr/share/nginx/html

EXPOSE 80

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -qO- http://127.0.0.1/ >/dev/null || exit 1
