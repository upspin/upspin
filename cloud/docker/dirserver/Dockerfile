FROM buildpack-deps:jessie-curl
COPY config /upspin/config
COPY public.upspinkey /upspin/public.upspinkey
COPY secret.upspinkey /upspin/secret.upspinkey
COPY bin/dirserver /dirserver
ENTRYPOINT ["/dirserver", "-config=/upspin/config", "-letscache=", "-kind=server", "-storeserveruser=STORESERVERUSER", "-serverconfig=userCacheSize=1000,logDir=/dirserver-logs", "-project=PROJECT", "-log=LOGLEVEL"]
EXPOSE 443
