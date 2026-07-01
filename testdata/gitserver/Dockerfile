FROM httpd:2.4-alpine@sha256:1b766f17b84026429b7cb243317b142921b24432336e798bc881c43f45ed9567

# git-http-backend ships in the git-daemon subpackage, not git itself.
RUN apk add --no-cache git=2.52.0-r0 git-daemon=2.52.0-r0

COPY httpd.conf /usr/local/apache2/conf/httpd.conf
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

EXPOSE 80
ENTRYPOINT ["/entrypoint.sh"]
