.DEFAULT_GOAL := build
PYTHON ?= python3
.PHONY: build check lint test install redeploy submission-check

build check lint test:
	$(PYTHON) validate.py

submission-check:
	$(PYTHON) validate.py --submission

install redeploy:
	@echo "This directory prepares sources only. Installation and deployment are separate authorized actions."
	@exit 2
