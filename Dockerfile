FROM alpine:latest as certs
RUN apk --update add ca-certificates

FROM scratch
ENTRYPOINT ["/again-scraper-go"]
COPY --from=certs /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY again-scraper-go /
