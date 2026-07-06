FROM httpd:2.4-alpine@sha256:0136c2d4462f3b8ecc92bea70efdfef4d06523999ae8d7aa533969dea6db4576

# git-http-backend ships in the git-daemon subpackage, not git itself.
RUN apk add --no-cache git=2.52.0-r0 git-daemon=2.52.0-r0

COPY httpd.conf /usr/local/apache2/conf/httpd.conf
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

EXPOSE 80
ENTRYPOINT ["/entrypoint.sh"]
