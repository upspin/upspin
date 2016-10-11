FROM gcr.io/PROJECT/base
COPY rc /upspin/rc
COPY storeserver /storeserver
ENV TMPDIR /
ENTRYPOINT ["/storeserver", "-context=/upspin/rc", "-kind=gcp", "-config=gcpProjectId=PROJECT,gcpBucketName=BUCKET,defaultACL=publicRead", "-project=PROJECT"]
EXPOSE 80 443
