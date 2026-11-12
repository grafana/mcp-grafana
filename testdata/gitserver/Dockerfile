FROM httpd:2.4-alpine@sha256:d72968666c0a6961b07036d85fc053bc3cf640583cb1ff8360e1617818f164dc

# git-http-backend ships in the git-daemon subpackage, not git itself.
RUN apk add --no-cache git git-daemon

COPY httpd.conf /usr/local/apache2/conf/httpd.conf
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

EXPOSE 80
ENTRYPOINT ["/entrypoint.sh"]
