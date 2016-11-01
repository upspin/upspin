FROM gcr.io/PROJECT/base
COPY rc /upspin/rc
COPY public.upspinkey /upspin/public.upspinkey
COPY secret.upspinkey /upspin/secret.upspinkey
COPY bin/storeserver /storeserver
ENV TMPDIR /
ENTRYPOINT ["/storeserver", "-context=/upspin/rc", "-kind=gcp", "-config=gcpProjectId=PROJECT,gcpBucketName=BUCKET,defaultACL=publicRead", "-project=PROJECT"]
EXPOSE 80 443
