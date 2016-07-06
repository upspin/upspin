FROM gcr.io/PROJECT/base
COPY serviceaccountkey.json /upspin/serviceaccountkey.json
ENV GOOGLE_APPLICATION_CREDENTIALS=/upspin/serviceaccountkey.json
COPY storeserver /storeserver
ENV TMPDIR /
ENTRYPOINT ["/storeserver", "-endpoint=gcp,local", "-config=gcpStore.CacheDir=/tmp/storecache,gcpProjectId=PROJECT,gcpBucketName=PROJECT-store,defaultACL=publicRead"]
EXPOSE 80 443
