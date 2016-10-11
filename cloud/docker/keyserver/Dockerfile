FROM gcr.io/PROJECT/base
COPY keyserver /keyserver
ENTRYPOINT ["/keyserver", "-kind=gcp", "-project=PROJECT", "-config=gcpProjectId=PROJECT,gcpBucketName=BUCKET,defaultACL=publicRead"]
EXPOSE 80 443
