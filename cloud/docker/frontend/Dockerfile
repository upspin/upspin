FROM buildpack-deps:jessie-curl
COPY bin/frontend /frontend
COPY src/upspin.io/doc /doc
COPY src/upspin.io/cmd/frontend/images /images
COPY src/upspin.io/cmd/frontend/templates /templates
ENTRYPOINT ["/frontend", "-docpath=/doc", "-http=:80", "-https=:443"]
EXPOSE 80 443
