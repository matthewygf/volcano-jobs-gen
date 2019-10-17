FROM golang:1.13.0-stretch

WORKDIR /go/src

RUN mkdir volcano-jobs-gen

COPY cmd volcano-jobs-gen/
COPY go.mod volcano-jobs-gen/
COPY go.sum volcano-jobs-gen/
WORKDIR /go/src/volcano-jobs-gen
RUN go build
