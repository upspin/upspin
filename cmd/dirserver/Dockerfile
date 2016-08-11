FROM gcr.io/PROJECT/base
COPY serviceaccountkey.json /upspin/serviceaccountkey.json
ENV GOOGLE_APPLICATION_CREDENTIALS=/upspin/serviceaccountkey.json
COPY rc /upspin/rc
COPY public.upspinkey /.ssh/public.upspinkey
COPY secret.upspinkey /.ssh/secret.upspinkey
COPY dirserver /dirserver
ENTRYPOINT ["/dirserver", "-context=/upspin/rc", "-endpoint=gcp,local", "-config=CONFIG", "-project=PROJECT"]
EXPOSE 80 443
