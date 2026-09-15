FROM golang:1.25-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG MAIN_PACKAGE=./cmd/server
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/drive-clone "$MAIN_PACKAGE"

FROM cockroachdb/cockroach:v23.2.0

# The administrative worker invokes this version's CLI over the network.
# Its glibc runtime is required; no Docker socket or remote shell is used.
ENV PATH="/cockroach:${PATH}"
USER 10001:10001
WORKDIR /app

COPY --from=build /out/drive-clone /app/drive-clone

EXPOSE 8080

ENTRYPOINT ["/app/drive-clone"]
CMD []
