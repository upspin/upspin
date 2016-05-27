FROM gcr.io/PROJECT/base
COPY userserver /userserver
ENTRYPOINT ["/userserver", "-endpoint=gcp,local", "-config=gcpProjectId=PROJECT,gcpBucketName=PROJECT-user"]
EXPOSE 8080 8443
