# Schema migrations with goose (MIT): https://github.com/pressly/goose
# Runs as a one-off compose service ("migrate") before the backend starts,
# connected as the schema owner. The backend itself only gets the
# least-privilege trackadoodle_app role and never runs DDL.
# goose v3.29 needs Go 1.26; this image is separate from the backend's.
FROM golang:1.26-alpine AS build
RUN CGO_ENABLED=0 go install -trimpath -ldflags="-s -w" \
      -tags='no_azuresql no_clickhouse no_libsql no_mssql no_mysql no_sqlite3 no_vertica no_ydb' \
      github.com/pressly/goose/v3/cmd/goose@v3.29.0

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /go/bin/goose /usr/local/bin/goose
COPY migrations/ /migrations/
ENV GOOSE_DRIVER=postgres \
    GOOSE_MIGRATION_DIR=/migrations
ENTRYPOINT ["goose"]
CMD ["up"]
