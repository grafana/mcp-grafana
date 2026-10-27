# Tasks moved to mise.toml. This stub forwards `make <target>` to
# `mise run <target>` for muscle memory and will be removed in a later release.

.DEFAULT_GOAL := help

.PHONY: help
help:
	@echo "Tasks now live in mise.toml: install mise (https://mise.jdx.dev/getting-started.html),"
	@echo "then run 'mise tasks' to list them and 'mise run <task>' to run one."

# An explicit phony rule per goal, not a `%:` pattern: make skips pattern rules
# for phony targets, and without .PHONY a file named like a task (build/) wins.
FORWARD := $(filter-out help,$(MAKECMDGOALS))
ifneq ($(FORWARD),)
.PHONY: $(FORWARD)
$(FORWARD):
	@command -v mise >/dev/null 2>&1 || { echo "mise is required: https://mise.jdx.dev/getting-started.html" >&2; exit 1; }
	@echo "make $@ is deprecated, use: mise run $@" >&2
	mise run $@
endif
