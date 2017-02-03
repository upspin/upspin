FROM buildpack-deps:jessie-curl
COPY mailconfig /mailconfig
COPY bin/keyserver /keyserver
ENTRYPOINT ["/keyserver", "-kind=server", "-project=PROJECT", "-mail_config=/mailconfig", "-serverconfig=gcpBucketName=BUCKET,defaultACL=projectPrivate", "-log=disabled"]
EXPOSE 80 443
