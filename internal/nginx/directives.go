package nginx

import "strings"

// The directive sets below must never be appended to in place; copy them
// first (as slices.Concat does).

// proxyDirectives pass information about the original request upstream.
var proxyDirectives = []directive{
	{"add_header", "X-Served-By $host"},
	{"proxy_set_header", "Host $host"},
	{"proxy_set_header", "X-Forwarded-Scheme $scheme"},
	{"proxy_set_header", "X-Forwarded-Proto $scheme"},
	{"proxy_set_header", "X-Forwarded-For $remote_addr"},
	{"proxy_set_header", "X-Real-IP $remote_addr"},
	{"proxy_set_header", "X-Real-Port $remote_port"},
}

var gzipDirectives = []directive{
	{"gzip", "on"},
	{"gzip_disable", "msie6"},
	{"gzip_vary", "on"},
	{"gzip_proxied", "any"},
	{"gzip_comp_level", "6"},
	{"gzip_buffers", "4 16k"},
	{"gzip_http_version", "1.1"},
	{"gzip_types", "application/atom+xml application/javascript application/x-javascript application/json application/rss+xml application/vnd.ms-fontobject application/x-font application/x-font-opentype application/x-font-otf application/x-font-truetype application/x-font-ttf application/x-web-app-manifest+json application/xhtml+xml application/xml font/opentype font/otf font/ttf image/svg+xml image/x-icon text/css text/plain text/javascript text/x-component text/xml"},
}

var noCacheDirectives = []directive{
	{"proxy_cache_bypass", "1"},
	{"proxy_no_cache", "1"},
	{"add_header", "Last-Modified $date_gmt"},
	{"add_header", `Cache-Control "no-store, no-cache, must-revalidate, proxy-revalidate, max-age=0"`},
	{"if_modified_since", "off"},
	{"expires", "off"},
	{"etag", "off"},
}

var realtimeDirectives = []directive{
	{"proxy_buffering", "off"},
	{"proxy_ignore_client_abort", "on"},
	{"proxy_socket_keepalive", "on"},
}

// websocketDirectives rely on the $connection_upgrade map defined at the top
// of the generated configuration.
var websocketDirectives = []directive{
	{"proxy_http_version", "1.1"},
	{"proxy_set_header", "Upgrade $http_upgrade"},
	{"proxy_set_header", "Connection $connection_upgrade"},
}

// tlsDirectives follow Mozilla's intermediate TLS configuration. OCSP
// stapling needs the CA chain, so it is only enabled if c names one.
func tlsDirectives(c Certificate) []directive {
	ds := []directive{
		{"ssl_certificate", quoteFile(c.Fullchain)},
		{"ssl_certificate_key", quoteFile(c.Key)},
	}
	if c.CA != "" {
		ds = append(ds, directive{"ssl_trusted_certificate", quote(c.CA)})
	}
	ds = append(ds, []directive{
		{"ssl_session_timeout", "1d"},
		{"ssl_session_cache", "shared:MozSSL:10m"},
		{"ssl_session_tickets", "off"},
		{"ssl_protocols", "TLSv1.2 TLSv1.3"},
		{"ssl_ciphers", "ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES128-GCM-SHA256:ECDHE-ECDSA-AES256-GCM-SHA384:ECDHE-RSA-AES256-GCM-SHA384:ECDHE-ECDSA-CHACHA20-POLY1305:ECDHE-RSA-CHACHA20-POLY1305:DHE-RSA-AES128-GCM-SHA256:DHE-RSA-AES256-GCM-SHA384"},
		{"ssl_prefer_server_ciphers", "off"},
	}...)
	if c.CA != "" {
		ds = append(ds, directive{"ssl_stapling", "on"}, directive{"ssl_stapling_verify", "on"})
	}
	return ds
}

// quoteFile quotes the path of a file, unless it is a reference to a
// certificate, as in raw configurations, which is expanded to a quoted path.
func quoteFile(path string) string {
	if strings.HasPrefix(path, "{{") {
		return path
	}
	return quote(path)
}
