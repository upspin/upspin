FROM gcr.io/PROJECT/base
COPY serviceaccountkey.json /upspin/serviceaccountkey.json
ENV GOOGLE_APPLICATION_CREDENTIALS=/upspin/serviceaccountkey.json
COPY rc.dirserver /upspin/rc.dirserver
COPY public.upspinkey /.ssh/public.upspinkey
COPY secret.upspinkey /.ssh/secret.upspinkey
COPY dirserver /dirserver
COPY config.gcp /config.gcp
ENTRYPOINT ["/dirserver", "-endpoint=gcp,local", "-configfile=/config.gcp"]
EXPOSE 80 443
