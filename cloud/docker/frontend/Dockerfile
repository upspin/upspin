FROM buildpack-deps:jessie-curl
COPY bin/frontend /frontend
COPY src/upspin.io/doc /
COPY src/upspin.io/cmd/frontend/images /
COPY src/upspin.io/cmd/frontend/templates /
ENTRYPOINT ["/frontend", "-docpath=./doc"]
EXPOSE 80 443
