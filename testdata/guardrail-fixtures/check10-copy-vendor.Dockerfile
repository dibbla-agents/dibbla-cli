# Check 9 fixture — MUST trip the check (BLOCKER).
#
# `vendor/` is one of the eight directories deploy-api strips from the upload
# archive before it becomes the build context. This Dockerfile builds fine
# locally and fails on the platform with:
#   "/vendor": not found in build context
FROM golang:1.22 AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY vendor/ ./vendor/
COPY . .
RUN go build -mod=vendor -o /app ./cmd/server

FROM gcr.io/distroless/base-debian12
COPY --from=build /app /app
EXPOSE 8080
ENTRYPOINT ["/app"]
