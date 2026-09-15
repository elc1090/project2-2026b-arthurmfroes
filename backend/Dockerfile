FROM golang:1.25-alpine AS build

WORKDIR /src

COPY go.mod ./

COPY . .

ARG MAIN_PACKAGE=./cmd/server
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/drive-clone "$MAIN_PACKAGE"

FROM alpine:3.22

RUN addgroup -S app && adduser -S app -G app
USER app
WORKDIR /app

COPY --from=build /out/drive-clone /app/drive-clone

EXPOSE 8080

ENTRYPOINT ["/app/drive-clone"]
