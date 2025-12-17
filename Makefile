APP_NAME=edgecdnx-api
TAG?=latest
IMAGE_NAME=$(APP_NAME):$(TAG)
PLATFORMS=linux/amd64,linux/arm64
DOCKER_REPO?=
FULL_IMAGE_NAME=$(DOCKER_REPO)/$(APP_NAME):$(TAG)

.PHONY: docker-build docker-tag docker-push

docker-build:
	docker buildx build --platform $(PLATFORMS) -t $(IMAGE_NAME) --load .

docker-tag:
	@if [ -z "$(DOCKER_REPO)" ]; then \
		echo "DOCKER_REPO environment variable not set"; \
		exit 1; \
	fi
	docker tag $(IMAGE_NAME) $(FULL_IMAGE_NAME)

docker-push: docker-tag
	@if [ -z "$(DOCKER_REPO)" ]; then \
		echo "DOCKER_REPO environment variable not set"; \
		exit 1; \
	fi
	docker push $(FULL_IMAGE_NAME) 