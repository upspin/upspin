FROM scratch
# Include protocols and services files so that the net package can
# resolve names like "tcp" and "https".
COPY protocols /etc/protocols
COPY services /etc/services
# Include root certs so that TLS works.
COPY ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
