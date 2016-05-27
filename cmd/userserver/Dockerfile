FROM gcr.io/PROJECT/base
COPY userserver /userserver
ENTRYPOINT ["/userserver", "-endpoint=gcp,local", "-config=gcpProjectId=PROJECT,gcpBucketName=PROJECT-user,defaultACL=projectPrivate"]
EXPOSE 80 443
