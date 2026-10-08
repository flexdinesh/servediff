FROM golang:1.25 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY packages/api ./packages/api
ARG TARGETOS=linux
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -o /servediff-server ./cmd/servediff-server
RUN mkdir /data && chown 10001:10001 /data

FROM scratch
COPY --from=build /servediff-server /servediff-server
COPY --from=build --chown=10001:10001 /data /data
USER 10001:10001
ENV SERVEDIFF_CONFIG_PATH=/data/config.json
ENV HOME=/data
VOLUME ["/data"]
EXPOSE 7981
STOPSIGNAL SIGTERM
ENTRYPOINT ["/servediff-server"]
CMD ["--listen", "0.0.0.0:7981", "--state", "/data/state.db"]
