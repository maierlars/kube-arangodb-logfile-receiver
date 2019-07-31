
.PHONY: all
all: build docker

.PHONY: build
build:
	go build

.PHONY: docker
docker:
	docker build -t malars/kube-arangodb-logfile-receiver .
	docker push malars/kube-arangodb-logfile-receiver