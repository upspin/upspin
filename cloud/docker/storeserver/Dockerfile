FROM buildpack-deps:jessie-curl
COPY config /upspin/config
COPY public.upspinkey /upspin/public.upspinkey
COPY secret.upspinkey /upspin/secret.upspinkey
COPY bin/storeserver /storeserver
ENV TMPDIR /
ENTRYPOINT ["/storeserver", "-config=/upspin/config", "-kind=gcp", "-serverconfig=gcpProjectId=PROJECT,gcpBucketName=BUCKET,defaultACL=publicRead", "-project=PROJECT"]
EXPOSE 80 443
