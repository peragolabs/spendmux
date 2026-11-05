# Nightly off-site backups of the Spendmux database. See docs/production.md.
FROM postgres:16.4-alpine@sha256:5660c2cbfea50c7a9127d17dc4e48543eedd3d7a41a595a2dfa572471e37e64c
RUN apk add --no-cache rclone age \
 && mkdir -p /backups && chown postgres:postgres /backups
COPY backup.sh restore.sh /usr/local/bin/
USER postgres
ENV HOME=/tmp RCLONE_CONFIG=/dev/null
CMD ["backup.sh", "schedule"]
