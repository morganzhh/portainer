
FROM alpine:latest
RUN apk --no-cache --update upgrade && apk --no-cache add ca-certificates

COPY dist /

VOLUME /data
WORKDIR /

EXPOSE 9000
EXPOSE 8000

ENTRYPOINT ["/portainer"]
