FROM buildpack-deps:jessie-curl
COPY rc /upspin/rc
COPY public.upspinkey /upspin/public.upspinkey
COPY secret.upspinkey /upspin/secret.upspinkey
COPY symmsecret.upspinkey /upspin/symmsecret.upspinkey
COPY bin/dirserver /dirserver
ENTRYPOINT ["/dirserver", "-config=/upspin/rc", "-kind=server", "-storeserveruser=STORESERVERUSER", "-serverconfig=userCacheSize=1000,logDir=/dirserver-logs", "-project=PROJECT", "-log=debug"]
EXPOSE 80 443
