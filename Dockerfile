# Dockerfile for talek
FROM golang:1.20
MAINTAINER Raymond Cheng <me@raymondcheng.net>
USER root

# Add the talek repository
ADD . "$GOPATH/src/github.com/privacylab/talek"
RUN ln -s "$GOPATH/src/github.com/privacylab/talek" /talek
WORKDIR /talek
