FROM gcr.io/PROJECT/base
COPY dirserver /dirserver
ENTRYPOINT ["/dirserver", "-endpoint=gcp,local", "-config=gcpProjectId=PROJECT,gcpBucketName=PROJECT-directory"]
EXPOSE 8080 8443
