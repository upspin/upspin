FROM scratch
COPY protocols /etc/protocols
COPY services /etc/services
COPY roots.crt /etc/ssl/certs/ca-certificates.crt
COPY frontend /frontend
ENTRYPOINT ["/frontend"]
EXPOSE 8080 8443
