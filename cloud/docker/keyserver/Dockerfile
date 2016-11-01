FROM gcr.io/PROJECT/base
COPY mailconfig /mailconfig
COPY bin/keyserver /keyserver
ENTRYPOINT ["/keyserver", "-kind=gcp", "-project=PROJECT", "-mail_config=/mailconfig", "-config=gcpProjectId=PROJECT,gcpBucketName=BUCKET,defaultACL=publicRead"]
EXPOSE 80 443
