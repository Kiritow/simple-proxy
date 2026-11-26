#!/bin/bash
# The entrypoint of the image: runs nginx and simple-proxy side by side, and
# the daily renewals of acme.sh. If nginx or simple-proxy exits, so does the
# container, for its restart policy to start it again.
#
# Arguments are flags for simple-proxy, which override the defaults below,
# e.g. -addr 127.0.0.1:81. Any other command runs instead, e.g. bash.
set -euo pipefail

if [[ $# -gt 0 && $1 != -* ]]; then
	exec "$@"
fi

conf=/etc/nginx/conf.d/simple-proxy.conf
webroot=/var/lib/simple-proxy/acme-challenge
acme=/opt/acme.sh/acme.sh
mkdir -p /data "$LE_CONFIG_HOME" "$webroot"

# The catch-all HTTPS server, which rejects unknown hosts, needs a
# certificate; any will do.
if [[ ! -f /data/fallback.key ]]; then
	openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -days 36500 \
		-subj /CN=invalid -keyout /data/fallback.key -out /data/fallback.cer 2>/dev/null
fi

# acme.sh defaults to ZeroSSL, which needs an account email.
"$acme" --set-default-ca --server "$ACME_SERVER" >/dev/null

# nginx starts without the sites, which simple-proxy applies when it starts:
# sites that nginx rejects must not keep nginx from starting, but be shown
# in the console to be fixed.
rm -f "$conf"
nginx -g 'daemon off;' &
nginx_pid=$!
# simple-proxy reloads nginx through its PID file.
for _ in {1..100}; do
	[[ -s /run/nginx.pid ]] && break
	kill -0 "$nginx_pid" 2>/dev/null || break
	sleep 0.1
done

simple-proxy \
	-addr :81 \
	-db /data/sites.db \
	-nginx-conf "$conf" \
	-acme "$acme" \
	-acme-webroot "$webroot" \
	-fallback-cert /data/fallback.cer \
	-fallback-key /data/fallback.key \
	"$@" &
proxy_pid=$!

# acme.sh renews the certificates that are due, and reloads nginx.
(while sleep 1d; do "$acme" --cron || true; done) &
renew_pid=$!

stop() {
	kill -QUIT "$nginx_pid" 2>/dev/null || true # gracefully
	kill -TERM "$proxy_pid" "$renew_pid" 2>/dev/null || true
}
stopped=0
trap 'stopped=1; stop' TERM INT

set +e
wait -n "$nginx_pid" "$proxy_pid"
status=$?
stop
wait "$nginx_pid" "$proxy_pid"
((stopped)) && status=0 # stopped as asked, e.g. by docker stop
exit "$status"
