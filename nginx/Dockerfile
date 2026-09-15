FROM nginx:alpine

COPY nginx.conf /etc/nginx/templates/nginx.conf.template
COPY entrypoint.sh /usr/local/bin/configure-and-start-nginx

RUN chmod +x /usr/local/bin/configure-and-start-nginx

ENTRYPOINT ["/usr/local/bin/configure-and-start-nginx"]
