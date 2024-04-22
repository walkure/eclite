FROM alpine:3.19.1
RUN apk add --no-cache \
	perl perl-cgi perl-yaml-syck perl-http-daemon perl-io

RUN addgroup -g 1000 -S nonroot && adduser -u 1000 -S nonroot -G nonroot
USER nonroot

WORKDIR /usr/src/eclite
COPY --chown=nonroot:nonroot SKSock.pm echonet.pl /usr/src/eclite

ENV ECLITE_CONFIG=/conf/config.yaml

ENTRYPOINT [ "perl","./echonet.pl"]
