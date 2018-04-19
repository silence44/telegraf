FROM golang:1.10

RUN apt-get update
RUN apt-get -y install curl

WORKDIR /go/src/github.com/influxdata/telegraf/
COPY . .

RUN make

ENTRYPOINT ["/go/src/github.com/influxdata/telegraf/telegraf"]