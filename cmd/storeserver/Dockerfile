FROM gcr.io/PROJECT/base
COPY serviceaccountkey.json /upspin/serviceaccountkey.json
ENV GOOGLE_APPLICATION_CREDENTIALS=/upspin/serviceaccountkey.json
COPY rc /upspin/rc
COPY storeserver /storeserver
ENV TMPDIR /
ENTRYPOINT ["/storeserver", "-context=/upspin/rc", "-kind=gcp", "-config=CONFIG", "-project=PROJECT"]
EXPOSE 80 443
