FROM golang:1.20 as build-env

WORKDIR /go/src/app
COPY . /go/src/app

WORKDIR /go/src/app/cmd/voibot
RUN go get
RUN CGO_ENABLED=0 go build -o /go/bin/voibot
RUN strip /go/bin/voibot

FROM gcr.io/distroless/static

COPY --from=build-env /go/bin/voibot /app/
CMD ["/app/voibot"]
