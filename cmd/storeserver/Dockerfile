FROM gcr.io/PROJECT/base
COPY storeserver /storeserver
MKDIR /tmp
ENTRYPOINT ["/storeserver", "-endpoint=gcp,local", "-config=gcpProjectId=PROJECT,gcpBucketName=PROJECT-store"]
EXPOSE 8080 8443
