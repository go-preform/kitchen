FROM golang
# Host is the address of the master node
# which is neccessary for the initial connection
ENV HOST_ADDR ''
ENV HOST_PORT 0
# Local host address for other workers to connect to
ENV LOCAL_ADDR tcp://0.0.0.0
ENV LOCAL_PORT 10001
# business portal address
ENV HTTP_ADDR 0.0.0.0
ENV HTTP_PORT 80
# always a good idea to run as non-root user
RUN groupadd -g 1000 workers
RUN useradd -u 1000 -g workers -s /bin/bash -m worker
USER 1000:1000
# copy the source code and build
WORKDIR /app
COPY . .
#RUN go mod tidy
#RUN go build -o app .
CMD ["./app"]