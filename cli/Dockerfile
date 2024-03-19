# Dockerfile for talek
FROM talek-base:latest
MAINTAINER Masala <masala@riseup.net>
USER root

# build and install talek cli tools
RUN go mod download
RUN cd cli/talekutil && go install
RUN cd cli/clinfo && go install
RUN cd cli/talekclient && go install
RUN cd cli/talekfrontend && go install
RUN cd cli/talekreplica && go install
RUN cd cli/talekutil && go install
