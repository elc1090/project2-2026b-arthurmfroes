FROM node:24-alpine AS frontend
WORKDIR /app
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

FROM nginx:alpine
RUN apk add --no-cache python3
COPY nginx/nginx.conf /etc/nginx/templates/nginx.conf.template
COPY nginx/reconcile.py /usr/local/bin/reconcile.py
COPY nginx/entrypoint.sh /usr/local/bin/configure-and-start-nginx
COPY --from=frontend /app/dist /usr/share/nginx/html
RUN chmod +x /usr/local/bin/configure-and-start-nginx
ENTRYPOINT ["/usr/local/bin/configure-and-start-nginx"]
