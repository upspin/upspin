# This is a Google Cloud Container Builder build step that extends the
# Go builder to just build the damn file. (The script as part of the
# go builder wants to do something else. Sigh.)

FROM golang:1.8
ENV GOPATH /workspace
ENTRYPOINT ["/usr/local/go/bin/go"]
