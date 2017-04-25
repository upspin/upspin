FROM buildpack-deps:jessie-curl
COPY bin/frontend /frontend
COPY src/upspin.io/doc /doc
ENTRYPOINT ["/frontend", "-letscache=", "-docpath=/doc", "-http=:80", "-https=:443"]
EXPOSE 80 443
