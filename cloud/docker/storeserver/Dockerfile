FROM buildpack-deps:jessie-curl
COPY rc /upspin/rc
COPY public.upspinkey /upspin/public.upspinkey
COPY secret.upspinkey /upspin/secret.upspinkey
COPY bin/storeserver /storeserver
ENV TMPDIR /
ENTRYPOINT ["/storeserver", "-config=/upspin/rc", "-kind=gcp", "-serverconfig=gcpProjectId=PROJECT,gcpBucketName=BUCKET,defaultACL=publicRead", "-project=PROJECT"]
EXPOSE 80 443
