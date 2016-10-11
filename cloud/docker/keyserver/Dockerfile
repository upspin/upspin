FROM gcr.io/PROJECT/base
COPY keyserver /keyserver
COPY emailconfig /emailconfig
ENTRYPOINT ["/keyserver", "-kind=gcp", "-project=PROJECT", "-email_config=/emailconfig", "-config=gcpProjectId=PROJECT,gcpBucketName=BUCKET,defaultACL=publicRead"]
EXPOSE 80 443
