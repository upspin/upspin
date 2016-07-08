FROM gcr.io/PROJECT/base
COPY keyserver /keyserver
ENTRYPOINT ["/keyserver", "-endpoint=gcp,local", "-config=gcpProjectId=PROJECT,gcpBucketName=PROJECT-user,defaultACL=projectPrivate"]
EXPOSE 80 443
