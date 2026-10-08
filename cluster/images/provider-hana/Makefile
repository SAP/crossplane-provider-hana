# ====================================================================================
# Setup Project

PLATFORMS := linux_amd64 linux_arm64
include ../../../build/makelib/common.mk

# ====================================================================================
#  Options
include ../../../build/makelib/imagelight.mk

# Commit time rather than wall-clock time keeps image labels reproducible
BUILD_TIMESTAMP ?= $(shell TZ=UTC git log -1 --date=format-local:%Y-%m-%dT%H:%M:%SZ --format=%cd)

# ====================================================================================
# Targets

img.build:
	@$(INFO) docker build $(IMAGE)
	@$(MAKE) BUILD_ARGS="--load $(BUILD_ARGS)" img.build.shared
	@$(OK) docker build $(IMAGE)

img.publish:
	@$(INFO) Skipping image publish for $(IMAGE)
	@echo Publish is deferred to xpkg machinery
	@$(OK) Image publish skipped for $(IMAGE)

img.build.shared:
	@cp Dockerfile $(IMAGE_TEMP_DIR) || $(FAIL)
	@cp -r $(OUTPUT_DIR)/bin/ $(IMAGE_TEMP_DIR)/bin || $(FAIL)
	@docker buildx build $(BUILD_ARGS) \
		--build-arg GIT_COMMIT=$(COMMIT_HASH) \
		--build-arg BUILD_TIMESTAMP=$(BUILD_TIMESTAMP) \
		--platform $(IMAGE_PLATFORMS) \
		-t $(IMAGE) \
		$(IMAGE_TEMP_DIR) || $(FAIL)

img.promote:
	@$(INFO) Skipping image promotion from $(FROM_IMAGE) to $(TO_IMAGE)
	@echo Promote is deferred to xpkg machinery
	@$(OK) Image promotion skipped for $(FROM_IMAGE) to $(TO_IMAGE)
