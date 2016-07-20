FROM gcr.io/PROJECT/base
COPY keyserver /keyserver
COPY serviceaccountkey.json /upspin/serviceaccountkey.json
ENV GOOGLE_APPLICATION_CREDENTIALS=/upspin/serviceaccountkey.json
ENTRYPOINT ["/keyserver", "-endpoint=gcp,local", "-project=PROJECT", "-config=gcpProjectId=PROJECT,gcpBucketName=PROJECT-key,defaultACL=projectPrivate"]
EXPOSE 80 443
