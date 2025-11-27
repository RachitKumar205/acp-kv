# build stage
FROM golang:1.24-alpine AS builder

# install build deps
RUN apk add --no-cache git

# set working dir
WORKDIR /build

#copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# copy source copde
COPY . .

# build bin
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o acp-node ./cmd/acp-node

# runtime stage
FROM alpine:latest

# Install network tools for chaos testing
RUN apk --no-cache add ca-certificates iproute2 iptables

WORKDIR /root/

# copy bin from builder
COPY --from=builder /build/acp-node .

#expose gRPC and metrics ports
EXPOSE 8080 9090

# run bin
CMD ["./acp-node"]
