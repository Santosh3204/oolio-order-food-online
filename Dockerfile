FROM golang:1.25 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/server . && CGO_ENABLED=0 go build -o /out/couponindex ./cmd/couponindex

FROM gcr.io/distroless/static
COPY --from=build /out/server /server
COPY --from=build /out/couponindex /couponindex
EXPOSE 8080
ENTRYPOINT ["/server"]
