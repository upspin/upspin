FROM buildpack-deps:jessie-curl
COPY mailconfig /mailconfig
COPY bin/keyserver /keyserver
ENTRYPOINT ["/keyserver", "-kind=gcp", "-project=PROJECT", "-mail_config=/mailconfig", "-serverconfig=gcpProjectId=PROJECT,gcpBucketName=BUCKET,defaultACL=projectPrivate", "-log=LOGLEVEL"]
EXPOSE 80 443
