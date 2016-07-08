FROM gcr.io/PROJECT/base
COPY serviceaccountkey.json /upspin/serviceaccountkey.json
ENV GOOGLE_APPLICATION_CREDENTIALS=/upspin/serviceaccountkey.json
COPY storeserver /storeserver
ENV TMPDIR /
ENTRYPOINT ["/storeserver", "-endpoint=gcp,local", "-project=PROJECT", "-keyendpoint=remote,key.upspin.io:443", "-config=gcpProjectId=PROJECT,gcpBucketName=PROJECT-store,defaultACL=publicRead"]
EXPOSE 80 443
