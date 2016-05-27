FROM gcr.io/PROJECT/base
COPY rc.dirserver /upspin/rc.dirserver
COPY public.upspinkey /.ssh/public.upspinkey
COPY secret.upspinkey /.ssh/secret.upspinkey
COPY dirserver /dirserver
ENTRYPOINT ["/dirserver", "-endpoint=gcp,local", "-config=gcpProjectId=PROJECT,gcpBucketName=PROJECT-directory"]
EXPOSE 8080 8443
