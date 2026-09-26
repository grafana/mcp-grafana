FROM httpd:2.4-alpine@sha256:4e585da9d0125dec36d4500a9f5c5df7b2c0a01f67cb47865a91a4b05bdbec1b

# git-http-backend ships in the git-daemon subpackage, not git itself.
RUN apk add --no-cache git=2.54.0-r0 git-daemon=2.54.0-r0

COPY httpd.conf /usr/local/apache2/conf/httpd.conf
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

EXPOSE 80
ENTRYPOINT ["/entrypoint.sh"]
