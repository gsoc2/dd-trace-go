FROM golang:1.21.8
COPY . /dd-trace-go
WORKDIR /dd-trace-go/internal/apps/unit-of-work
RUN go get -t
RUN go install
CMD ./run.bash
