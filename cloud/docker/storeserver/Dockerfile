FROM buildpack-deps:jessie-curl
COPY config /upspin/config
COPY public.upspinkey /upspin/public.upspinkey
COPY secret.upspinkey /upspin/secret.upspinkey
COPY bin/storeserver /storeserver
ENV TMPDIR /
ENTRYPOINT ["/storeserver", "-letscache=", "-config=/upspin/config", "-kind=server", "-serverconfig=backend=GCS,gcpBucketName=BUCKET,defaultACL=publicRead", "-project=PROJECT", "-log=LOGLEVEL"]
EXPOSE 443
