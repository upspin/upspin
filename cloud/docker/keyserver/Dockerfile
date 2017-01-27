FROM buildpack-deps:jessie-curl
COPY mailconfig /mailconfig
COPY bin/keyserver /keyserver
ENTRYPOINT ["/keyserver", "-kind=gcp", "-project=PROJECT", "-mail_config=/mailconfig", "-serverconfig=gcpBucketName=BUCKET,defaultACL=projectPrivate", "-log=LOGLEVEL"]
EXPOSE 80 443
