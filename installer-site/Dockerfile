# The `install` app: the origin behind https://install.dibbla.com.
#
# Caddy (not nginx) to match the dibbla-docs app, the other Caddy-on-:80 static
# app on this platform — same shape, same port, one thing to know instead of two.
# Nothing is built here: `public/` is staged by ./stage.sh, which is what both
# the `mirror` release job and a hand-run cutover call.
FROM caddy:2-alpine

COPY Caddyfile /etc/caddy/Caddyfile
COPY public/ /srv/

# Fail the build instead of deploying an empty mirror.
#
# `public/` is generated, not committed, so a `docker build` (or a `dibbla
# deploy`) from a tree where stage.sh has not run would otherwise succeed and
# publish a site that 404s every install. Since install.dibbla.com is on the
# critical path for every new install — including the dibbla-docs deploy job,
# which installs the CLI from it — that failure has to be loud and early.
RUN set -eu; \
    for f in install.sh install.ps1 latest.json checksums.txt; do \
        [ -s "/srv/$f" ] || { echo "installer-site: /srv/$f missing or empty — run ./stage.sh <tag> first" >&2; exit 1; }; \
    done; \
    [ -n "$(ls -A /srv/latest 2>/dev/null)" ] || { echo "installer-site: /srv/latest is empty — run ./stage.sh <tag> first" >&2; exit 1; }; \
    caddy validate --config /etc/caddy/Caddyfile

EXPOSE 80
