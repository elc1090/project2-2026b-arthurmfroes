#!/bin/sh
set -eu

: "${BACKEND_NODES:?Defina BACKEND_NODES com os enderecos dos backends separados por espaco}"

DNS_RESOLVER="$(awk '/^nameserver[[:space:]]/{print $2; exit}' /etc/resolv.conf)"

if [ -z "$DNS_RESOLVER" ]; then
    echo "Nao foi possivel descobrir o resolvedor DNS" >&2
    exit 1
fi

case "$DNS_RESOLVER" in
    *:*) DNS_RESOLVER="[$DNS_RESOLVER]" ;;
esac
export DNS_RESOLVER

servers_file="$(mktemp)"
config_file="$(mktemp)"
trap 'rm -f "$servers_file" "$config_file"' EXIT

for backend in $BACKEND_NODES; do
    case "$backend" in
        *[!a-zA-Z0-9._:-]*)
            echo "Endereco de backend invalido: $backend" >&2
            exit 1
            ;;
    esac

    printf '        server %s resolve max_fails=1 fail_timeout=5s;\n' "$backend" >> "$servers_file"
done

sed '/# BACKEND_SERVERS/r '"$servers_file" /etc/nginx/templates/nginx.conf.template > "$config_file"
envsubst '${DNS_RESOLVER}' < "$config_file" > /etc/nginx/nginx.conf

nginx -t
exec nginx -g 'daemon off;'
