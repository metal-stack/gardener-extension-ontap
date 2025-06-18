FROM golang:1.24 AS builder

WORKDIR /go/src/github.com/metal-stack/gardener-extension-ontap
COPY . .
RUN make install \
 && strip /go/bin/gardener-extension-ontap

FROM alpine:3.22
WORKDIR /
COPY charts /charts
COPY --from=builder /go/bin/gardener-extension-ontap /gardener-extension-ontap
CMD ["/gardener-extension-ontap"]
