FROM gcr.io/PROJECT/base
COPY serviceaccountkey.json /upspin/serviceaccountkey.json
ENV GOOGLE_APPLICATION_CREDENTIALS=/upspin/serviceaccountkey.json
COPY rc /upspin/rc
COPY public.upspinkey /upspin/public.upspinkey
COPY secret.upspinkey /upspin/secret.upspinkey
COPY dirserver /dirserver
ENTRYPOINT ["/dirserver", "-context=/upspin/rc", "-endpoint=gcp,local", "-config=CONFIG", "-project=PROJECT"]
EXPOSE 80 443
