FROM buildpack-deps:jessie-curl
COPY config /upspin/config
COPY public.upspinkey /upspin/public.upspinkey
COPY secret.upspinkey /upspin/secret.upspinkey
COPY mailconfig /upspin/mailconfig
COPY bin/keyserver /keyserver
ENTRYPOINT ["/keyserver", "-letscache=", "-config=/upspin/config", "-kind=server", "-project=PROJECT", "-mail_config=/upspin/mailconfig", "-serverconfig=gcpBucketName=BUCKET,defaultACL=projectPrivate", "-log=LOGLEVEL"]
EXPOSE 443
